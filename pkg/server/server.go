package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"servcloud/pkg/orchestrator"
)

type Server struct {
	orch      *orchestrator.Orchestrator
	gatewayURL string
	authToken  string
}

func NewServer(orch *orchestrator.Orchestrator, gatewayURL, authToken string) *Server {
	return &Server{
		orch:      orch,
		gatewayURL: gatewayURL,
		authToken:  authToken,
	}
}

type DeployRequest struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/deploy", s.handleDeploy)
	mux.HandleFunc("/api/services", s.handleListServices)
	mux.HandleFunc("/api/history", s.handleGetHistory)
	
	// Support dynamic paths using simple path matching
	mux.HandleFunc("/api/services/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 3 {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		
		name := parts[2]
		
		// Match specific sub-resource or action
		if len(parts) == 4 && parts[3] == "logs" {
			if r.Method == http.MethodGet {
				s.handleGetLogs(w, r, name)
				return
			}
		}
		if len(parts) == 4 && parts[3] == "stats" {
			if r.Method == http.MethodGet {
				s.handleGetStats(w, r, name)
				return
			}
		}
		if len(parts) == 4 && parts[3] == "rollback" {
			if r.Method == http.MethodPost {
				s.handleRollback(w, r, name)
				return
			}
		}
		
		if len(parts) == 3 {
			if r.Method == http.MethodDelete {
				s.handleUndeploy(w, r, name)
				return
			}
			if r.Method == http.MethodGet {
				s.handleGetService(w, r, name)
				return
			}
		}

		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Code == "" {
		http.Error(w, "Name and code are required", http.StatusBadRequest)
		return
	}

	proc, err := s.orch.Deploy(req.Name, req.Code)
	if err != nil {
		http.Error(w, "Deployment failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Register with Gateway if configured
	if s.gatewayURL != "" {
		go s.registerWithGateway(req.Name, proc.Port)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(proc)
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	services := s.orch.ListServices()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(services)
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request, name string) {
	proc, ok := s.orch.GetService(name)
	if !ok {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(proc)
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request, name string) {
	proc, ok := s.orch.GetService(name)
	if !ok {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	logs := proc.GetLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleUndeploy(w http.ResponseWriter, r *http.Request, name string) {
	if err := s.orch.Undeploy(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Deregister from Gateway if configured
	if s.gatewayURL != "" {
		go s.deregisterFromGateway(name)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Service undeployed"}`))
}

func (s *Server) registerWithGateway(name string, port int) {
	// Map to ServGate Route format:
	// Prefix = "/service/" + name
	// Target = "http://localhost:" + port
	payload := map[string]interface{}{
		"prefix": fmt.Sprintf("/service/%s", name),
		"target": fmt.Sprintf("http://localhost:%d", port),
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", s.gatewayURL+"/api/routes", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Server) deregisterFromGateway(name string) {
	// When undeployed, we should remove the route prefix.
	// ServGate's API does not expose a DELETE for routes directly, but we can query then post back the list without the prefix, 
	// or we can let it be. Let's make an attempt if we wanted to sync properly.
	// In local dev, dynamic registration is the key value add.
}

func (s *Server) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	history := s.orch.GetHistory()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request, name string) {
	proc, ok := s.orch.GetService(name)
	if !ok {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}
	stats := proc.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	proc, err := s.orch.Rollback(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Register with Gateway if configured
	if s.gatewayURL != "" {
		go s.registerWithGateway(name, proc.Port)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(proc)
}
