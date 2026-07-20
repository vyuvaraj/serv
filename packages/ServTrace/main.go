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

	"github.com/vyuvaraj/serv/packages/ServTrace/pkg/server"
	"github.com/vyuvaraj/serv/packages/ServTrace/pkg/store"
)

const version = "0.1.0"

func main() {
	port := flag.Int("port", 8090, "OTLP/HTTP collector and API listen port")
	limit := flag.Int("limit", 1000, "Maximum number of traces to keep in memory")
	verFlag := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *verFlag {
		fmt.Printf("ServTrace Collector v%s\n", version)
		return
	}

	log.Printf("Starting ServTrace Collector v%s...", version)
	log.Printf("Trace limit in memory: %d", *limit)

	ts := store.NewStore(*limit)
	
	// Setup Cold Tier Archiver callback
	SetupColdTierArchiver(ts)

	srv := server.NewServer(ts)
	
	httpSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("ServTrace OTLP/HTTP listening on http://localhost:%d", *port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Trace collector server failed: %v", err)
		}
	}()

	// Graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("Shutting down ServTrace Collector gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Collector forced shutdown: %v", err)
	}

	log.Println("ServTrace Collector shutdown complete.")
}
