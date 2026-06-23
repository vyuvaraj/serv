package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"servcron/pkg/cron"
	"servcron/pkg/otel"
	"servcron/pkg/server"
)

func main() {
	addr := flag.String("addr", ":8087", "ServCron listening address")
	redisURL := flag.String("redis-url", "", "Redis URL for distributed leader election (e.g. redis://localhost:6379)")
	lockKey := flag.String("redis-lock-key", "servcron:leader:lock", "Redis key for leader lease lock")
	leaseDur := flag.Duration("redis-lease-duration", 15*time.Second, "Lease duration for leader election")
	flag.Parse()

	// Override with env variables if set
	if envPort := os.Getenv("PORT"); envPort != "" {
		*addr = ":" + envPort
	}
	if envRedis := os.Getenv("REDIS_URL"); envRedis != "" {
		*redisURL = envRedis
	}
	if envLockKey := os.Getenv("REDIS_LOCK_KEY"); envLockKey != "" {
		*lockKey = envLockKey
	}
	if envLease := os.Getenv("REDIS_LEASE_DURATION"); envLease != "" {
		if d, err := time.ParseDuration(envLease); err == nil {
			*leaseDur = d
		}
	}

	log.Printf("Starting ServCron service on %s...", *addr)

	// Initialize tracing
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	otel.InitTrace(ctx, "servcron")
	defer otel.Shutdown(context.Background())

	// Initialize components
	elector := cron.NewLeaderElector(*redisURL, *lockKey, *leaseDur)
	scheduler := cron.NewScheduler(elector.IsLeader)
	srv := server.NewServer(scheduler, elector)

	// Start components
	elector.Start()
	defer elector.Stop()

	scheduler.Start()
	defer scheduler.Stop()

	// Set up server
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: mux,
	}

	go func() {
		log.Printf("ServCron server listening on http://localhost%s", *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ServCron HTTP server failed: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	log.Println("ServCron: Shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("ServCron: HTTP server forced shutdown: %v", err)
	} else {
		log.Println("ServCron: HTTP server exited cleanly")
	}
}
