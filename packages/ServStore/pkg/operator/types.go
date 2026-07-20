// Package operator contains the Go type definitions for the ServStore
// Custom Resource Definitions (CRDs). These types implement runtime.Object
// and are registered with the controller-runtime scheme.
package operator

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ─── ServStoreCluster ───────────────────────────────────────────────────────

// ServStoreClusterSpec defines the desired state of a ServStore cluster.
type ServStoreClusterSpec struct {
	// Replicas is the number of storage nodes to deploy (1–32).
	Replicas int32 `json:"replicas"`
	// Image is the container image for the ServStore server.
	Image string `json:"image"`
	// DataDir is the mount path for object data inside the container.
	// +optional
	DataDir string `json:"dataDir,omitempty"`
	// Auth configures S3 authentication.
	// +optional
	Auth *ClusterAuth `json:"auth,omitempty"`
	// TLS configures TLS 1.3 for the S3 endpoint.
	// +optional
	TLS *ClusterTLS `json:"tls,omitempty"`
	// ErasureCoding enables Reed-Solomon erasure coding.
	// +optional
	ErasureCoding *ErasureCodingConfig `json:"erasureCoding,omitempty"`
	// ReplicationFactor is the number of data replicas (used when erasure coding is disabled).
	// +optional
	ReplicationFactor int32 `json:"replicationFactor,omitempty"`
	// RateLimit configures per-tenant request rate limiting.
	// +optional
	RateLimit *RateLimitConfig `json:"rateLimit,omitempty"`
	// Storage configures the PersistentVolumeClaim for each node.
	// +optional
	Storage *StorageConfig `json:"storage,omitempty"`
}

type ClusterAuth struct {
	Enabled              bool   `json:"enabled"`
	AccessKey            string `json:"accessKey,omitempty"`
	SecretKeySecretRef   *SecretKeyRef `json:"secretKeySecretRef,omitempty"`
}

type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type ClusterTLS struct {
	SecretName string `json:"secretName"`
}

type ErasureCodingConfig struct {
	Enabled      bool  `json:"enabled"`
	DataShards   int32 `json:"dataShards,omitempty"`
	ParityShards int32 `json:"parityShards,omitempty"`
}

type RateLimitConfig struct {
	RequestsPerSecond int `json:"requestsPerSecond"`
	Burst             int `json:"burst,omitempty"`
}

type StorageConfig struct {
	StorageClassName string `json:"storageClassName,omitempty"`
	Size             string `json:"size,omitempty"`
}

// ClusterPhase is the lifecycle state of a ServStoreCluster.
type ClusterPhase string

const (
	ClusterPhasePending      ClusterPhase = "Pending"
	ClusterPhaseInitializing ClusterPhase = "Initializing"
	ClusterPhaseRunning      ClusterPhase = "Running"
	ClusterPhaseUpgrading    ClusterPhase = "Upgrading"
	ClusterPhaseDegraded     ClusterPhase = "Degraded"
	ClusterPhaseFailed       ClusterPhase = "Failed"
)

// ServStoreClusterStatus reflects the observed state of a ServStoreCluster.
type ServStoreClusterStatus struct {
	Phase      ClusterPhase       `json:"phase,omitempty"`
	ReadyNodes int32              `json:"readyNodes,omitempty"`
	Endpoint   string             `json:"endpoint,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ServStoreCluster is the Schema for the servstoreclusters API.
type ServStoreCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServStoreClusterSpec   `json:"spec,omitempty"`
	Status            ServStoreClusterStatus `json:"status,omitempty"`
}

func (s *ServStoreCluster) DeepCopyObject() runtime.Object {
	out := new(ServStoreCluster)
	*out = *s
	out.Spec = s.Spec
	out.Status = s.Status
	return out
}

// ServStoreClusterList is a list of ServStoreCluster resources.
type ServStoreClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServStoreCluster `json:"items"`
}

func (s *ServStoreClusterList) DeepCopyObject() runtime.Object {
	out := new(ServStoreClusterList)
	*out = *s
	out.Items = make([]ServStoreCluster, len(s.Items))
	copy(out.Items, s.Items)
	return out
}

// ─── ServStoreBucket ────────────────────────────────────────────────────────

// ServStoreBucketSpec defines the desired state of a managed bucket.
type ServStoreBucketSpec struct {
	ClusterRef         string                `json:"clusterRef"`
	Versioning         string                `json:"versioning,omitempty"`
	ContentAddressable bool                  `json:"contentAddressable,omitempty"`
	DeletionPolicy     string                `json:"deletionPolicy,omitempty"`
	Lifecycle          []BucketLifecycleRule `json:"lifecycle,omitempty"`
	ColdTier           *BucketColdTier       `json:"coldTier,omitempty"`
}

type BucketLifecycleRule struct {
	ID             string `json:"id"`
	Enabled        bool   `json:"enabled"`
	Prefix         string `json:"prefix,omitempty"`
	ExpirationDays int    `json:"expirationDays"`
}

type BucketColdTier struct {
	Endpoint        string        `json:"endpoint"`
	RemoteBucket    string        `json:"remoteBucket"`
	Region          string        `json:"region,omitempty"`
	SecretRef       *SecretKeyRef `json:"secretRef,omitempty"`
	MinAgeDays      int           `json:"minAgeDays,omitempty"`
	ScanIntervalMin int           `json:"scanIntervalMin,omitempty"`
}

// BucketPhase is the lifecycle state of a ServStoreBucket.
type BucketPhase string

const (
	BucketPhasePending BucketPhase = "Pending"
	BucketPhaseReady   BucketPhase = "Ready"
	BucketPhaseFailed  BucketPhase = "Failed"
)

// ServStoreBucketStatus reflects the observed state of a ServStoreBucket.
type ServStoreBucketStatus struct {
	Phase    BucketPhase `json:"phase,omitempty"`
	Endpoint string      `json:"endpoint,omitempty"`
	Message  string      `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ServStoreBucket is the Schema for the servstorebuckets API.
type ServStoreBucket struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServStoreBucketSpec   `json:"spec,omitempty"`
	Status            ServStoreBucketStatus `json:"status,omitempty"`
}

func (s *ServStoreBucket) DeepCopyObject() runtime.Object {
	out := new(ServStoreBucket)
	*out = *s
	return out
}

// ServStoreBucketList is a list of ServStoreBucket resources.
type ServStoreBucketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServStoreBucket `json:"items"`
}

func (s *ServStoreBucketList) DeepCopyObject() runtime.Object {
	out := new(ServStoreBucketList)
	*out = *s
	out.Items = make([]ServStoreBucket, len(s.Items))
	copy(out.Items, s.Items)
	return out
}

// ─── ServStoreCredential ────────────────────────────────────────────────────

// ServStoreCredentialSpec defines the desired state of a managed credential.
type ServStoreCredentialSpec struct {
	ClusterRef string `json:"clusterRef"`
	SecretRef  string `json:"secretRef"`
	Username   string `json:"username,omitempty"`
	Policy     string `json:"policy,omitempty"`
}

// CredentialPhase is the sync state of a ServStoreCredential.
type CredentialPhase string

const (
	CredentialPhasePending CredentialPhase = "Pending"
	CredentialPhaseSynced  CredentialPhase = "Synced"
	CredentialPhaseFailed  CredentialPhase = "Failed"
)

// ServStoreCredentialStatus reflects the sync state.
type ServStoreCredentialStatus struct {
	Phase        CredentialPhase `json:"phase,omitempty"`
	LastSyncTime *metav1.Time    `json:"lastSyncTime,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ServStoreCredential is the Schema for the servstorecredentials API.
type ServStoreCredential struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServStoreCredentialSpec   `json:"spec,omitempty"`
	Status            ServStoreCredentialStatus `json:"status,omitempty"`
}

func (s *ServStoreCredential) DeepCopyObject() runtime.Object {
	out := new(ServStoreCredential)
	*out = *s
	return out
}

// ServStoreCredentialList is a list of ServStoreCredential resources.
type ServStoreCredentialList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServStoreCredential `json:"items"`
}

func (s *ServStoreCredentialList) DeepCopyObject() runtime.Object {
	out := new(ServStoreCredentialList)
	*out = *s
	out.Items = make([]ServStoreCredential, len(s.Items))
	copy(out.Items, s.Items)
	return out
}
