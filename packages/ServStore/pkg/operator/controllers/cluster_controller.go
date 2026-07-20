package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/api/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operv1 "github.com/vyuvaraj/serv/packages/ServStore/pkg/operator"
)

// ServStoreClusterReconciler reconciles a ServStoreCluster object
type ServStoreClusterReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=storage.servstore.io,resources=servstoreclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.servstore.io,resources=servstoreclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete

func (r *ServStoreClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("servstorecluster", req.NamespacedName)

	// Fetch the ServStoreCluster instance
	var cluster operv1.ServStoreCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 1. Reconcile headless Service for internal DNS and gossip peer discovery
	headlessSvcName := cluster.Name + "-headless"
	var headlessSvc corev1.Service
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: headlessSvcName}, &headlessSvc)
	if err != nil && errors.IsNotFound(err) {
		svc := r.newHeadlessService(&cluster)
		log.Info("Creating headless service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		if err := r.Create(ctx, svc); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 2. Reconcile main external access Service
	svcName := cluster.Name
	var mainSvc corev1.Service
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: svcName}, &mainSvc)
	if err != nil && errors.IsNotFound(err) {
		svc := r.newMainService(&cluster)
		log.Info("Creating S3 service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		if err := r.Create(ctx, svc); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 3. Reconcile PodDisruptionBudget for HA guarantees
	pdbName := cluster.Name
	var pdb policyv1.PodDisruptionBudget
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: pdbName}, &pdb)
	if err != nil && errors.IsNotFound(err) {
		newPdb := r.newPdb(&cluster)
		log.Info("Creating PodDisruptionBudget", "PDB.Namespace", newPdb.Namespace, "PDB.Name", newPdb.Name)
		if err := r.Create(ctx, newPdb); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 4. Reconcile StatefulSet
	stsName := cluster.Name
	var sts appsv1.StatefulSet
	err = r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: stsName}, &sts)
	if err != nil && errors.IsNotFound(err) {
		newSts := r.newStatefulSet(&cluster)
		log.Info("Creating StatefulSet", "StatefulSet.Namespace", newSts.Namespace, "StatefulSet.Name", newSts.Name)
		if err := r.Create(ctx, newSts); err != nil {
			return ctrl.Result{}, err
		}
		cluster.Status.Phase = operv1.ClusterPhaseInitializing
		if err := r.Status().Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check if spec changed and update StatefulSet
	desiredSts := r.newStatefulSet(&cluster)
	if *sts.Spec.Replicas != *desiredSts.Spec.Replicas || sts.Spec.Template.Spec.Containers[0].Image != desiredSts.Spec.Template.Spec.Containers[0].Image {
		log.Info("Updating StatefulSet spec (scaling or upgrading)", "StatefulSet.Namespace", sts.Namespace, "StatefulSet.Name", sts.Name)
		sts.Spec.Replicas = desiredSts.Spec.Replicas
		sts.Spec.Template.Spec.Containers[0].Image = desiredSts.Spec.Template.Spec.Containers[0].Image
		sts.Spec.Template.Spec.Containers[0].Args = desiredSts.Spec.Template.Spec.Containers[0].Args
		sts.Spec.Template.Spec.Containers[0].Env = desiredSts.Spec.Template.Spec.Containers[0].Env
		if err := r.Update(ctx, &sts); err != nil {
			return ctrl.Result{}, err
		}
		cluster.Status.Phase = operv1.ClusterPhaseUpgrading
		_ = r.Status().Update(ctx, &cluster)
		return ctrl.Result{Requeue: true}, nil
	}

	// Update Status fields
	readyReplicas := sts.Status.ReadyReplicas
	cluster.Status.ReadyNodes = readyReplicas
	cluster.Status.Endpoint = fmt.Sprintf("http://%s.%s.svc.cluster.local:9000", cluster.Name, cluster.Namespace)
	if cluster.Spec.TLS != nil && cluster.Spec.TLS.SecretName != "" {
		cluster.Status.Endpoint = fmt.Sprintf("https://%s.%s.svc.cluster.local:9000", cluster.Name, cluster.Namespace)
	}

	if readyReplicas == cluster.Spec.Replicas {
		cluster.Status.Phase = operv1.ClusterPhaseRunning
	} else if readyReplicas == 0 {
		cluster.Status.Phase = operv1.ClusterPhasePending
	} else {
		cluster.Status.Phase = operv1.ClusterPhaseDegraded
	}

	if err := r.Status().Update(ctx, &cluster); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ServStoreClusterReconciler) newHeadlessService(c *operv1.ServStoreCluster) *corev1.Service {
	labels := map[string]string{
		"app.kubernetes.io/name":     "github.com/vyuvaraj/serv/packages/ServStore",
		"app.kubernetes.io/instance": c.Name,
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name + "-headless",
			Namespace: c.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"service.alpha.kubernetes.io/tolerate-unready-endpoints": "true",
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(c, operv1.GroupVersion.WithKind("ServStoreCluster")),
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                corev1.ClusterIPNone,
			PublishNotReadyAddresses: true,
			Selector:                 labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "s3",
					Port:       9000,
					TargetPort: intstr.FromString("s3"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

func (r *ServStoreClusterReconciler) newMainService(c *operv1.ServStoreCluster) *corev1.Service {
	labels := map[string]string{
		"app.kubernetes.io/name":     "github.com/vyuvaraj/serv/packages/ServStore",
		"app.kubernetes.io/instance": c.Name,
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name,
			Namespace: c.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(c, operv1.GroupVersion.WithKind("ServStoreCluster")),
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "s3",
					Port:       9000,
					TargetPort: intstr.FromString("s3"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

func (r *ServStoreClusterReconciler) newPdb(c *operv1.ServStoreCluster) *policyv1.PodDisruptionBudget {
	labels := map[string]string{
		"app.kubernetes.io/name":     "github.com/vyuvaraj/serv/packages/ServStore",
		"app.kubernetes.io/instance": c.Name,
	}
	minAvail := intstr.FromInt32(c.Spec.Replicas - 1)
	if minAvail.IntVal < 1 {
		minAvail = intstr.FromInt32(1)
	}
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name,
			Namespace: c.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(c, operv1.GroupVersion.WithKind("ServStoreCluster")),
			},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvail,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
		},
	}
}

func (r *ServStoreClusterReconciler) newStatefulSet(c *operv1.ServStoreCluster) *appsv1.StatefulSet {
	labels := map[string]string{
		"app.kubernetes.io/name":     "github.com/vyuvaraj/serv/packages/ServStore",
		"app.kubernetes.io/instance": c.Name,
	}
	replicas := c.Spec.Replicas

	maxUnavail := intstr.FromInt32(1)
	updateStrategy := appsv1.StatefulSetUpdateStrategy{
		Type: appsv1.RollingUpdateStatefulSetStrategyType,
		RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
			MaxUnavailable: &maxUnavail,
		},
	}

	dataDir := c.Spec.DataDir
	if dataDir == "" {
		dataDir = "/data"
	}

	args := []string{
		"--port=9000",
		fmt.Sprintf("--data-dir=%s", dataDir),
	}

	// Handle ErasureCoding / Replication
	if c.Spec.ErasureCoding != nil && c.Spec.ErasureCoding.Enabled {
		args = append(args, "--erasure-coding")
		dataShards := c.Spec.ErasureCoding.DataShards
		if dataShards == 0 {
			dataShards = 2
		}
		parityShards := c.Spec.ErasureCoding.ParityShards
		if parityShards == 0 {
			parityShards = 1
		}
		args = append(args, fmt.Sprintf("--data-shards=%d", dataShards), fmt.Sprintf("--parity-shards=%d", parityShards))
	} else {
		repFactor := c.Spec.ReplicationFactor
		if repFactor == 0 {
			repFactor = 2
		}
		args = append(args, fmt.Sprintf("--replication-factor=%d", repFactor))
	}

	// Handle Rate Limiting
	if c.Spec.RateLimit != nil && c.Spec.RateLimit.RequestsPerSecond > 0 {
		args = append(args, fmt.Sprintf("--rate-limit-rps=%d", c.Spec.RateLimit.RequestsPerSecond))
		burst := c.Spec.RateLimit.Burst
		if burst == 0 {
			burst = c.Spec.RateLimit.RequestsPerSecond * 2
		}
		args = append(args, fmt.Sprintf("--rate-limit-burst=%d", burst))
	}

	// TLS Setup inside args
	if c.Spec.TLS != nil && c.Spec.TLS.SecretName != "" {
		args = append(args, "--tls-cert=/tls/tls.crt", "--tls-key=/tls/tls.key")
	}

	// Peer identification env vars and peer args
	args = append(args,
		fmt.Sprintf("--node-addr=$(POD_NAME).%s-headless.%s.svc.cluster.local:9000", c.Name, c.Namespace),
		"--node-id=$(POD_NAME)",
	)

	env := []corev1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
	}

	if c.Spec.Auth != nil && c.Spec.Auth.Enabled {
		args = append(args, "--auth", "--access-key=$(ACCESS_KEY)", "--secret-key=$(SECRET_KEY)")
		env = append(env, corev1.EnvVar{
			Name:  "ACCESS_KEY",
			Value: c.Spec.Auth.AccessKey,
		})
		if c.Spec.Auth.SecretKeySecretRef != nil {
			env = append(env, corev1.EnvVar{
				Name: "SECRET_KEY",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: c.Spec.Auth.SecretKeySecretRef.Name,
						},
						Key: c.Spec.Auth.SecretKeySecretRef.Key,
					},
				},
			})
		}
	}

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "data",
			MountPath: dataDir,
		},
	}

	volumes := []corev1.Volume{}

	if c.Spec.TLS != nil && c.Spec.TLS.SecretName != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "tls",
			MountPath: "/tls",
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: c.Spec.TLS.SecretName,
				},
			},
		})
	}

	volumeClaims := []corev1.PersistentVolumeClaim{}

	storageSize := "10Gi"
	if c.Spec.Storage != nil && c.Spec.Storage.Size != "" {
		storageSize = c.Spec.Storage.Size
	}

	var storageClass *string
	if c.Spec.Storage != nil && c.Spec.Storage.StorageClassName != "" {
		storageClass = &c.Spec.Storage.StorageClassName
	}

	// Persistent PVC template
	qty := resource.MustParse(storageSize)
	volumeClaims = append(volumeClaims, corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "data",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: qty,
				},
			},
			StorageClassName: storageClass,
		},
	})

	runAsUser := int64(1000)
	fsGroup := int64(1000)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name,
			Namespace: c.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(c, operv1.GroupVersion.WithKind("ServStoreCluster")),
			},
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName:         c.Name + "-headless",
			Replicas:            &replicas,
			UpdateStrategy:      updateStrategy,
			PodManagementPolicy: appsv1.OrderedReadyPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser: &runAsUser,
						FSGroup:   &fsGroup,
					},
					Containers: []corev1.Container{
						{
							Name:            "github.com/vyuvaraj/serv/packages/ServStore",
							Image:           c.Spec.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            args,
							Env:             env,
							Ports: []corev1.ContainerPort{
								{
									Name:          "s3",
									ContainerPort: 9000,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
			VolumeClaimTemplates: volumeClaims,
		},
	}
}

func (r *ServStoreClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operv1.ServStoreCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Complete(r)
}
