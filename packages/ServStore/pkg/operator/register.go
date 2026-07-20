package operator

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "storage.servstore.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme

	// Scheme is the runtime scheme for the operator
	Scheme = runtime.NewScheme()
)

func init() {
	SchemeBuilder.Register(addKnownTypes)
	_ = AddToScheme(Scheme)
}

// Adds the list of known types to Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&ServStoreCluster{},
		&ServStoreClusterList{},
		&ServStoreBucket{},
		&ServStoreBucketList{},
		&ServStoreCredential{},
		&ServStoreCredentialList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
