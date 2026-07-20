package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operv1 "github.com/vyuvaraj/serv/packages/ServStore/pkg/operator"
)

func TestClusterReconciler_Basic(t *testing.T) {
	// Register schema
	scheme := runtime.NewScheme()
	_ = operv1.AddToScheme(scheme)

	// Create a mock ServStoreCluster resource
	cluster := &operv1.ServStoreCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: operv1.ServStoreClusterSpec{
			Replicas: 3,
			Image:    "servstore:latest",
		},
	}

	// Create fake Kubernetes client
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()

	r := &ServStoreClusterReconciler{
		Client: fakeClient,
		Log:    logr.Discard(),
	}

	// Reconcile
	_ = context.Background()
	_ = client.ObjectKey{Namespace: "default", Name: "test-cluster"}
	
	// Ensure we can build statefulset template specifications
	sts := r.newStatefulSet(cluster)
	if *sts.Spec.Replicas != 3 {
		t.Errorf("expected 3 replicas in statefulset, got %d", *sts.Spec.Replicas)
	}

	headlessSvc := r.newHeadlessService(cluster)
	if headlessSvc.Name != "test-cluster-headless" {
		t.Errorf("expected headless service name to be test-cluster-headless, got %s", headlessSvc.Name)
	}

	mainSvc := r.newMainService(cluster)
	if mainSvc.Name != "test-cluster" {
		t.Errorf("expected service name to be test-cluster, got %s", mainSvc.Name)
	}
}
