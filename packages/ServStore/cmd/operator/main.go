// Package operator implements Kubernetes operators for ServStore.
// This file contains the main entry point for the operator binary.
package main

import (
    "flag"
    "fmt"
    "os"
    "sigs.k8s.io/controller-runtime/pkg/client/config"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/log/zap"
    ctrlhealthz "sigs.k8s.io/controller-runtime/pkg/healthz"
    operv1 "servstore/pkg/operator"
    "servstore/pkg/operator/controllers"
)

func main() {
    var metricsAddr string
    var enableLeaderElection bool
    var probeAddr string
    flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
    flag.StringVar(&probeAddr, "health-probe-addr", ":8081", "The address the probe endpoint binds to.")
    flag.BoolVar(&enableLeaderElection, "leader-elect", false,
        "Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
    flag.Parse()

    ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

    cfg, err := config.GetConfig()
    if err != nil {
        fmt.Fprintf(os.Stderr, "unable to get kubeconfig: %v\n", err)
        os.Exit(1)
    }

    mgr, err := ctrl.NewManager(cfg, ctrl.Options{
        Scheme:             operv1.Scheme,
        LeaderElection:     enableLeaderElection,
        LeaderElectionID:   "servstore-operator-leader-election",
        HealthProbeBindAddress: probeAddr,
    })
    if err != nil {
        fmt.Fprintf(os.Stderr, "unable to start manager: %v\n", err)
        os.Exit(1)
    }

    // Setup controllers
    if err = (&controllers.ServStoreClusterReconciler{Client: mgr.GetClient(), Log: ctrl.Log.WithName("controllers").WithName("ServStoreCluster")}).SetupWithManager(mgr); err != nil {
        fmt.Fprintf(os.Stderr, "unable to create controller ServStoreCluster: %v\n", err)
        os.Exit(1)
    }
    if err = (&controllers.ServStoreBucketReconciler{Client: mgr.GetClient(), Log: ctrl.Log.WithName("controllers").WithName("ServStoreBucket")}).SetupWithManager(mgr); err != nil {
        fmt.Fprintf(os.Stderr, "unable to create controller ServStoreBucket: %v\n", err)
        os.Exit(1)
    }
    if err = (&controllers.ServStoreCredentialReconciler{Client: mgr.GetClient(), Log: ctrl.Log.WithName("controllers").WithName("ServStoreCredential")}).SetupWithManager(mgr); err != nil {
        fmt.Fprintf(os.Stderr, "unable to create controller ServStoreCredential: %v\n", err)
        os.Exit(1)
    }

    // Add health and ready checks
    if err := mgr.AddHealthzCheck("healthz", ctrlhealthz.Ping); err != nil {
        fmt.Fprintf(os.Stderr, "unable to set up health check: %v\n", err)
        os.Exit(1)
    }
    if err := mgr.AddReadyzCheck("readyz", ctrlhealthz.Ping); err != nil {
        fmt.Fprintf(os.Stderr, "unable to set up ready check: %v\n", err)
        os.Exit(1)
    }

    fmt.Println("starting ServStore operator manager")
    if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
        fmt.Fprintf(os.Stderr, "problem running manager: %v\n", err)
        os.Exit(1)
    }
}
