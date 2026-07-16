package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vyuvaraj/ServShared"
	"servflow/pkg/handlers"
	"servflow/pkg/storage"
)

var (
	definitions = make(map[string]storage.WorkflowDef)
	instances   = make(map[string]*storage.WorkflowInstance)
	mu          sync.RWMutex
)

var workflowStore storage.WorkflowStore

func initStore(dbDriver, dbURL string) {
	if dbURL != "" {
		drv := dbDriver
		if drv == "" {
			drv = "sqlite"
		}
		store, err := storage.NewSQLWorkflowStore(drv, dbURL)
		if err != nil {
			log.Fatalf("failed to initialize SQL workflow store: %v", err)
		}
		workflowStore = store
		log.Printf("[INFO] ServFlow using SQL database storage backend: driver=%s", drv)
	} else {
		client := ServShared.NewStoreClient()
		workflowStore = storage.NewServStoreWorkflowStore(client)
	}
	loadStateFromStore()
}

func loadStateFromStore() {
	if defs, err := workflowStore.LoadDefinitions(); err == nil {
		mu.Lock()
		definitions = defs
		mu.Unlock()
	}
	if insts, err := workflowStore.LoadInstances(); err == nil {
		mu.Lock()
		instances = insts
		mu.Unlock()
	}
}

func getContext() *handlers.HandlerContext {
	return &handlers.HandlerContext{
		Definitions:   definitions,
		Instances:     instances,
		Mu:            &mu,
		WorkflowStore: workflowStore,
	}
}

func handleDefine(w http.ResponseWriter, r *http.Request) {
	getContext().HandleDefine(w, r)
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	getContext().HandleExecute(w, r)
}

func handleResume(w http.ResponseWriter, r *http.Request) {
	getContext().HandleResume(w, r)
}

func handleApprove(w http.ResponseWriter, r *http.Request) {
	getContext().HandleApprove(w, r)
}

func handleGetInstance(w http.ResponseWriter, r *http.Request) {
	getContext().HandleGetInstance(w, r)
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	getContext().HandleHistory(w, r)
}

func handleTimeTravelReplay(w http.ResponseWriter, r *http.Request) {
	getContext().HandleTimeTravelReplay(w, r)
}

func handleReplay(w http.ResponseWriter, r *http.Request) {
	getContext().HandleReplay(w, r)
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	getContext().HandleValidate(w, r)
}

func handleVisualize(w http.ResponseWriter, r *http.Request) {
	getContext().HandleVisualize(w, r)
}

func handleCompensateComplete(w http.ResponseWriter, r *http.Request) {
	getContext().HandleCompensateComplete(w, r)
}

func handleDesignerSave(w http.ResponseWriter, r *http.Request) {
	getContext().HandleDesignerSave(w, r)
}

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	getContext().HandleGenerate(w, r)
}

func main() {
	portStr := flag.String("port", "8096", "ServFlow server port")
	dbDriverStr := flag.String("database-driver", "", "Database driver (sqlite, postgres, mysql)")
	dbURLStr := flag.String("database-url", "", "Database URL/DSN connection string")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	dbDriver := os.Getenv("DATABASE_DRIVER")
	if dbDriver == "" {
		dbDriver = *dbDriverStr
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = *dbURLStr
	}

	standalone := ServShared.IsStandalone()
	if standalone {
		log.Println("[INFO] ServFlow: Running in standalone mode. Store persistence redirected to local directory.")
	}

	initStore(dbDriver, dbURL)

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servflow", "1.0.0"))
	mux.HandleFunc("/api/v1/version", ServShared.VersionHandler("servflow", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/resume", handleResume)
	mux.HandleFunc("/api/workflows/approve", handleApprove)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)
	mux.HandleFunc("/api/instances/", handleTimeTravelReplay) // DX.13: time-travel replay — GET /api/instances/{id}/replay
	mux.HandleFunc("/api/workflows/history", handleHistory)
	mux.HandleFunc("/api/workflows/replay", handleReplay)
	mux.HandleFunc("/api/workflows/validate", handleValidate)
	mux.HandleFunc("/api/workflows/visualize", handleVisualize)
	mux.HandleFunc("/api/workflows/compensate/complete", handleCompensateComplete)
	mux.HandleFunc("/api/workflows/designer/save", handleDesignerSave)
	mux.HandleFunc("/api/workflows/generate", handleGenerate)

	// Wrapper handler for /api/v1/ prefix rewriting (V1.1 support)
	v1Wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
		}
		mux.ServeHTTP(w, r)
	})

	rateLimiter := ServShared.RateLimitMiddleware
	if flag.Lookup("test.v") != nil {
		rateLimiter = func(next http.Handler) http.Handler {
			return next
		}
	}

	// Wrap in ServShared middleware: Trace -> RateLimit -> CORS -> MaxBytes -> Auth -> Tenant -> v1Wrapper
	serverHandler := ServShared.TraceMiddleware("servflow",
		rateLimiter(
			ServShared.CORSMiddleware(
				ServShared.MaxBytesMiddleware(10*1024*1024)(
					ServShared.AuthMiddleware(
						ServShared.TenantMiddleware(v1Wrapper),
					),
				),
			),
		),
	)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: serverHandler,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[INFO] ServFlow engine starting on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start ServFlow: %v", err)
		}
	}()

	<-stop

	log.Println("[INFO] Shutting down ServFlow server...")
	ServShared.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
	log.Println("[INFO] ServFlow server exited cleanly")
}
