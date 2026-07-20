package controllers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operv1 "github.com/vyuvaraj/serv/packages/ServStore/pkg/operator"
)

// ServStoreBucketReconciler reconciles a ServStoreBucket object
type ServStoreBucketReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=storage.servstore.io,resources=servstorebuckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.servstore.io,resources=servstorebuckets/status,verbs=get;update;patch

func (r *ServStoreBucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("servstorebucket", req.NamespacedName)

	var bucket operv1.ServStoreBucket
	if err := r.Get(ctx, req.NamespacedName, &bucket); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve the target cluster
	var cluster operv1.ServStoreCluster
	clusterKey := client.ObjectKey{Namespace: bucket.Namespace, Name: bucket.Spec.ClusterRef}
	if err := r.Get(ctx, clusterKey, &cluster); err != nil {
		log.Error(err, "Referenced cluster not found", "clusterRef", bucket.Spec.ClusterRef)
		bucket.Status.Phase = operv1.BucketPhaseFailed
		bucket.Status.Message = fmt.Sprintf("Cluster %s not found", bucket.Spec.ClusterRef)
		_ = r.Status().Update(ctx, &bucket)
		return ctrl.Result{}, err
	}

	if cluster.Status.Phase != operv1.ClusterPhaseRunning {
		log.Info("Cluster is not ready yet; requeueing", "cluster", cluster.Name, "phase", cluster.Status.Phase)
		bucket.Status.Phase = operv1.BucketPhasePending
		bucket.Status.Message = "Waiting for cluster running state"
		_ = r.Status().Update(ctx, &bucket)
		return ctrl.Result{RequeueAfter: 10 * 1000 * 1000 * 1000}, nil // 10 seconds
	}

	// 1. Call ServStore Cluster S3 API to create/ensure the S3 Bucket.
	// Since the model uses standard Go client under the hood, we can issue S3 HTTP requests
	// directly to the local cluster endpoint status.Endpoint.
	endpoint := cluster.Status.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("http://%s.%s.svc.cluster.local:9000", cluster.Name, cluster.Namespace)
	}

	// Perform S3 operations to create bucket, set versioning, configure cold tiering, etc.
	// In order to be robust and fully decoupled, we call HTTP endpoints on the cluster.
	err := r.ensureBucket(ctx, endpoint, &bucket)
	if err != nil {
		log.Error(err, "Failed to reconcile bucket configuration on cluster S3 endpoint")
		bucket.Status.Phase = operv1.BucketPhaseFailed
		bucket.Status.Message = err.Error()
		_ = r.Status().Update(ctx, &bucket)
		return ctrl.Result{}, err
	}

	bucket.Status.Phase = operv1.BucketPhaseReady
	bucket.Status.Endpoint = fmt.Sprintf("%s/%s", endpoint, bucket.Name)
	bucket.Status.Message = "S3 bucket fully provisioned"
	if err := r.Status().Update(ctx, &bucket); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ServStoreBucketReconciler) ensureBucket(ctx context.Context, endpoint string, b *operv1.ServStoreBucket) error {
	client := &http.Client{}

	// PUT request to create the bucket
	url := fmt.Sprintf("%s/%s", endpoint, b.Name)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, nil)
	if err != nil {
		return err
	}

	if b.Spec.ContentAddressable {
		req.Header.Set("X-ServStore-Content-Addressable", "true")
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("S3 endpoint communication failure: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		// Conflict is fine (bucket already exists)
		return fmt.Errorf("failed to create bucket: status %d", resp.StatusCode)
	}

	// Configure versioning
	versioningVal := b.Spec.Versioning
	if versioningVal == "" {
		versioningVal = "Disabled"
	}
	vUrl := fmt.Sprintf("%s/%s?versioning", endpoint, b.Name)
	vBody := fmt.Sprintf(`<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, versioningVal)
	vReq, err := http.NewRequestWithContext(ctx, "PUT", vUrl, strings.NewReader(vBody))
	if err != nil {
		return err
	}
	vResp, err := client.Do(vReq)
	if err == nil {
		vResp.Body.Close()
	}

	// Configure Cold Tier if specified
	if b.Spec.ColdTier != nil {
		cUrl := fmt.Sprintf("%s/%s?cold-tier", endpoint, b.Name)
		// Send JSON/Form parameters for configuration
		cBody := fmt.Sprintf(`{"endpoint":"%s","remoteBucket":"%s","region":"%s","minAgeDays":%d,"scanIntervalMin":%d}`,
			b.Spec.ColdTier.Endpoint, b.Spec.ColdTier.RemoteBucket, b.Spec.ColdTier.Region, b.Spec.ColdTier.MinAgeDays, b.Spec.ColdTier.ScanIntervalMin)
		cReq, err := http.NewRequestWithContext(ctx, "PUT", cUrl, strings.NewReader(cBody))
		if err != nil {
			return err
		}
		cReq.Header.Set("Content-Type", "application/json")
		cResp, err := client.Do(cReq)
		if err == nil {
			cResp.Body.Close()
		}
	}

	return nil
}

func (r *ServStoreBucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operv1.ServStoreBucket{}).
		Complete(r)
}
