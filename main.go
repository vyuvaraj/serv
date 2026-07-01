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

	"servmesh/pkg/registry"
)

const version = "0.1.0"

func main() {
	port := flag.Int("port", 8089, "Registry listen port")
	ttlSec := flag.Int("ttl", 10, "Service instance heartbeat TTL in seconds")
	verFlag := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *verFlag {
		fmt.Printf("ServMesh Registry v%s\n", version)
		return
	}

	log.Printf("Starting ServMesh Registry v%s on port :%d...", version, *port)
	
	r := registry.NewRegistry(time.Duration(*ttlSec) * time.Second)
	
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: r.Handler(),
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Registry server failed: %v", err)
		}
	}()

	// Graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("Shutting down ServMesh Registry gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Registry forced shutdown: %v", err)
	}

	r.Close()
	log.Println("ServMesh Registry shutdown complete.")
}
