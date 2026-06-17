package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"servgate/pkg/otel"
	"servgate/pkg/proxy"
	"servgate/pkg/wasm"
)

type Config struct {
	Addr      string        `json:"addr"`
	AuthToken string        `json:"auth_token"`
	TlsCert   string        `json:"tls_cert"`
	TlsKey    string        `json:"tls_key"`
	Routes    []proxy.Route `json:"routes"`
}

func main() {
	// Initialize distributed tracing
	otel.Init()

	configFile, err := os.Open("config.json")
	if err != nil {
		log.Fatalf("Gateway: failed to open config: %v", err)
	}
	defer configFile.Close()

	var cfg Config
	if err := json.NewDecoder(configFile).Decode(&cfg); err != nil {
		log.Fatalf("Gateway: failed to parse config: %v", err)
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		log.Fatalf("Gateway: failed to start WASM: %v", err)
	}

	handler := proxy.NewGatewayHandler(cfg.Routes, wasmManager, cfg.AuthToken)

	// Admin API endpoint to dynamically register WASM middlewares on the fly
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("/api/admin/middleware/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		
		// Auth check for admin registration
		if cfg.AuthToken != "" {
			if r.Header.Get("Authorization") != "Bearer "+cfg.AuthToken {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 5 {
			http.Error(w, "Invalid path. Use /api/admin/middleware/{name}", http.StatusBadRequest)
			return
		}
		name := parts[4]

		wasmBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		err = wasmManager.Register(r.Context(), name, wasmBytes)
		if err != nil {
			http.Error(w, "Failed to compile WASM: "+err.Error(), http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("WASM Middleware " + name + " compiled and registered"))
	})

	log.Printf("Starting ServGate reverse proxy on %s...", cfg.Addr)
	server := &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
	}

	if cfg.TlsCert != "" && cfg.TlsKey != "" {
		if err := server.ListenAndServeTLS(cfg.TlsCert, cfg.TlsKey); err != nil {
			log.Fatalf("Gateway: HTTP server error: %v", err)
		}
	} else {
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("Gateway: HTTP server error: %v", err)
		}
	}
}
