package controllers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operv1 "github.com/vyuvaraj/serv/packages/ServStore/pkg/operator"
)

// ServStoreCredentialReconciler reconciles a ServStoreCredential object
type ServStoreCredentialReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=storage.servstore.io,resources=servstorecredentials,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.servstore.io,resources=servstorecredentials/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ServStoreCredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("servstorecredential", req.NamespacedName)

	var cred operv1.ServStoreCredential
	if err := r.Get(ctx, req.NamespacedName, &cred); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve the target cluster
	var cluster operv1.ServStoreCluster
	clusterKey := client.ObjectKey{Namespace: cred.Namespace, Name: cred.Spec.ClusterRef}
	if err := r.Get(ctx, clusterKey, &cluster); err != nil {
		log.Error(err, "Referenced cluster not found", "clusterRef", cred.Spec.ClusterRef)
		cred.Status.Phase = operv1.CredentialPhaseFailed
		cred.Status.Message = fmt.Sprintf("Cluster %s not found", cred.Spec.ClusterRef)
		_ = r.Status().Update(ctx, &cred)
		return ctrl.Result{}, err
	}

	if cluster.Status.Phase != operv1.ClusterPhaseRunning {
		log.Info("Cluster is not ready yet; requeueing", "cluster", cluster.Name)
		cred.Status.Phase = operv1.CredentialPhasePending
		cred.Status.Message = "Waiting for cluster running state"
		_ = r.Status().Update(ctx, &cred)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Fetch referenced Secret
	var secret corev1.Secret
	secretKey := client.ObjectKey{Namespace: cred.Namespace, Name: cred.Spec.SecretRef}
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		log.Error(err, "Referenced credential Secret not found", "secretRef", cred.Spec.SecretRef)
		cred.Status.Phase = operv1.CredentialPhaseFailed
		cred.Status.Message = fmt.Sprintf("Secret %s not found", cred.Spec.SecretRef)
		_ = r.Status().Update(ctx, &cred)
		return ctrl.Result{}, err
	}

	accessKey := string(secret.Data["access-key"])
	secretKeyData := string(secret.Data["secret-key"])
	if accessKey == "" || secretKeyData == "" {
		err := fmt.Errorf("secret missing access-key or secret-key data fields")
		cred.Status.Phase = operv1.CredentialPhaseFailed
		cred.Status.Message = err.Error()
		_ = r.Status().Update(ctx, &cred)
		return ctrl.Result{}, err
	}

	endpoint := cluster.Status.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("http://%s.%s.svc.cluster.local:9000", cluster.Name, cluster.Namespace)
	}

	username := cred.Spec.Username
	if username == "" {
		username = cred.Spec.SecretRef
	}

	// Calls S3 Gateway user management policy API on the cluster to sync credentials/policy
	err := r.syncCredentialToCluster(ctx, endpoint, username, accessKey, secretKeyData, cred.Spec.Policy)
	if err != nil {
		log.Error(err, "Failed to sync credentials to cluster")
		cred.Status.Phase = operv1.CredentialPhaseFailed
		cred.Status.Message = err.Error()
		_ = r.Status().Update(ctx, &cred)
		return ctrl.Result{}, err
	}

	now := metav1.Now()
	cred.Status.Phase = operv1.CredentialPhaseSynced
	cred.Status.LastSyncTime = &now
	cred.Status.Message = "S3 credentials and policy successfully synced to cluster"
	if err := r.Status().Update(ctx, &cred); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ServStoreCredentialReconciler) syncCredentialToCluster(ctx context.Context, endpoint, username, accessKey, secretKey, policy string) error {
	client := &http.Client{}

	// ServStore S3 Gateway user policy creation API
	url := fmt.Sprintf("%s/?user-policy&user=%s", endpoint, username)
	
	// Create JSON payload mapping credentials and authorization policy
	payload := fmt.Sprintf(`{"accessKey":"%s","secretKey":"%s","policy":%s}`,
		accessKey, secretKey, getPolicyJSON(policy))
	
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("S3 user-policy sync failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 status when syncing credentials: %d", resp.StatusCode)
	}

	return nil
}

func getPolicyJSON(policy string) string {
	if policy == "" {
		return `{"Effect":"Allow","Action":["s3:*"],"Resource":["*"]}`
	}
	return policy
}

func (r *ServStoreCredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operv1.ServStoreCredential{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
