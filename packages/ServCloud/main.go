package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vyuvaraj/serv/packages/ServCloud/pkg/orchestrator"
	"github.com/vyuvaraj/serv/packages/ServCloud/pkg/otel"
	"github.com/vyuvaraj/serv/packages/ServCloud/pkg/server"
)

const version = "0.1.0"

func main() {
	port := flag.Int("port", 8085, "Port to listen on")
	workDir := flag.String("workdir", "./.deployments", "Directory for deployments and builds")
	gatewayURL := flag.String("gateway", "http://localhost:8080", "ServGate URL for dynamic route sync")
	authToken := flag.String("auth-token", "secret-token", "Auth token for Gateway registration")
	verFlag := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *verFlag {
		fmt.Printf("ServCloud v%s\n", version)
		return
	}

	// Initialize OpenTelemetry
	otel.Init()

	log.Printf("Starting ServCloud v%s...", version)
	log.Printf("Work directory: %s", *workDir)
	if *gatewayURL != "" {
		log.Printf("ServGate sync enabled at: %s", *gatewayURL)
	}

	orch, err := orchestrator.NewOrchestrator(*workDir)
	if err != nil {
		log.Fatalf("Failed to initialize orchestrator: %v", err)
	}

	srv := server.NewServer(orch, *gatewayURL, *authToken)
	httpSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("ServCloud listening on http://localhost:%d", *port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server listen failed: %v", err)
		}
	}()

	// Wait for termination signal
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("Shutting down ServCloud gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown server first
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	// Undeploy all running services to clean up process resources
	for _, svc := range orch.ListServices() {
		log.Printf("Stopping running service during shutdown: %s", svc.Name)
		_ = orch.Undeploy(svc.Name)
	}

	log.Println("ServCloud shutdown complete.")
}
