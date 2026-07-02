package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
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

func initStore() {
	client := ServShared.NewStoreClient()
	workflowStore = storage.NewServStoreWorkflowStore(client)
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

func main() {
	portStr := flag.String("port", "8096", "ServFlow server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	initStore()

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servflow", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/resume", handleResume)
	mux.HandleFunc("/api/workflows/approve", handleApprove)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)
	mux.HandleFunc("/api/workflows/history", handleHistory)
	mux.HandleFunc("/api/workflows/replay", handleReplay)
	mux.HandleFunc("/api/workflows/validate", handleValidate)
	mux.HandleFunc("/api/workflows/visualize", handleVisualize)
	mux.HandleFunc("/api/workflows/compensate/complete", handleCompensateComplete)

	serverHandler := ServShared.TraceMiddleware("servflow", ServShared.AuthMiddleware(mux))

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
