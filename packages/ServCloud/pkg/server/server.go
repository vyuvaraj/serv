package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"servcloud/pkg/orchestrator"

	"github.com/vyuvaraj/ServShared"
)

type Server struct {
	orch            *orchestrator.Orchestrator
	gatewayURL      string
	authToken       string
	autoscaleTicker *time.Ticker
}

func NewServer(orch *orchestrator.Orchestrator, gatewayURL, authToken string) *Server {
	srv := &Server{
		orch:      orch,
		gatewayURL: gatewayURL,
		authToken:  authToken,
	}
	srv.StartAutoscaleLoop()
	return srv
}

type DeployRequest struct {
	Name   string `json:"name"`
	Code   string `json:"code"`
	Branch string `json:"branch,omitempty"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servcloud", "1.0.0"))
	mux.HandleFunc("/api/v1/version", ServShared.VersionHandler("servcloud", "1.0.0"))
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/deploy", s.handleDeploy)
	mux.HandleFunc("/api/services", s.handleListServices)
	mux.HandleFunc("/api/history", s.handleGetHistory)
	
	// Support dynamic paths using simple path matching
	mux.HandleFunc("/api/services/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 3 {
			writeJSONError(w, r, "Not Found", http.StatusNotFound)
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
		if len(parts) == 4 && parts[3] == "env" {
			if r.Method == http.MethodPost {
				s.handleUpdateEnv(w, r, name)
				return
			}
		}
		
		if len(parts) == 4 && parts[3] == "invoke" {
			s.handleInvoke(w, r, name)
			return
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
 
		writeJSONError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	// Wrapper handler for /api/v1/ prefix rewriting (V1.1 support)
	v1Wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
		}
		mux.ServeHTTP(w, r)
	})

	// Wrap in ServShared middleware: Trace -> RateLimit -> CORS -> MaxBytes -> Auth -> Tenant -> v1Wrapper
	return ServShared.TraceMiddleware("servcloud",
		ServShared.RateLimitMiddleware(
			ServShared.CORSMiddleware(
				ServShared.MaxBytesMiddleware(10*1024*1024)(
					ServShared.AuthMiddleware(
						ServShared.TenantMiddleware(v1Wrapper),
					),
				),
			),
		),
	)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, r, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Code == "" {
		writeJSONError(w, r, "Name and code are required", http.StatusBadRequest)
		return
	}

	branch := req.Branch
	if branch == "" {
		branch = r.Header.Get("X-Branch-Name")
	}

	var proc *orchestrator.ServiceProcess
	var err error
	if branch != "" {
		proc, err = s.orch.DeployPreview(req.Name, req.Code, branch, nil)
	} else {
		proc, err = s.orch.Deploy(req.Name, req.Code)
	}

	if err != nil {
		writeJSONError(w, r, "Deployment failed: "+err.Error(), http.StatusInternalServerError)
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
		writeJSONError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	services := s.orch.ListServices()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(services)
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request, name string) {
	proc, ok := s.orch.GetService(name)
	if !ok {
		writeJSONError(w, r, "Service not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(proc)
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request, name string) {
	proc, ok := s.orch.GetService(name)
	if !ok {
		writeJSONError(w, r, "Service not found", http.StatusNotFound)
		return
	}

	logs := proc.GetLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleUndeploy(w http.ResponseWriter, r *http.Request, name string) {
	if err := s.orch.Undeploy(name); err != nil {
		writeJSONError(w, r, err.Error(), http.StatusNotFound)
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
		writeJSONError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	history := s.orch.GetHistory()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request, name string) {
	proc, ok := s.orch.GetService(name)
	if !ok {
		writeJSONError(w, r, "Service not found", http.StatusNotFound)
		return
	}
	stats := proc.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	proc, err := s.orch.Rollback(name)
	if err != nil {
		writeJSONError(w, r, err.Error(), http.StatusBadRequest)
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

func (s *Server) handleUpdateEnv(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var newEnv map[string]string
	if err := json.NewDecoder(r.Body).Decode(&newEnv); err != nil {
		writeJSONError(w, r, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Fetch current service process to get its srvCode code
	proc, ok := s.orch.GetService(name)
	if !ok {
		writeJSONError(w, r, "Service not found", http.StatusNotFound)
		return
	}

	// Fetch deployment history to retrieve the code snapshot
	history := s.orch.GetHistory()
	var code string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ServiceName == name {
			code = history[i].Code
			break
		}
	}

	if code == "" {
		writeJSONError(w, r, "No code snapshot found to deploy", http.StatusInternalServerError)
		return
	}

	// Trigger rolling deployment with the new dynamic env overrides!
	// We merge existing env with the new env variables
	mergedEnv := make(map[string]string)
	for k, v := range proc.Env {
		mergedEnv[k] = v
	}
	for k, v := range newEnv {
		mergedEnv[k] = v
	}

	newProc, err := s.orch.DeployWithEnv(name, code, mergedEnv)
	if err != nil {
		writeJSONError(w, r, "Rolling update with new environment variables failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Register with Gateway if configured
	if s.gatewayURL != "" {
		go s.registerWithGateway(name, newProc.Port)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(newProc)
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request, name string) {
	proc, ok := s.orch.GetService(name)
	if !ok {
		writeJSONError(w, r, "Service not found", http.StatusNotFound)
		return
	}

	if proc.Status == "stopped" || proc.Status == "failed" {
		history := s.orch.GetHistory()
		var code string
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].ServiceName == name {
				code = history[i].Code
				break
			}
		}
		if code == "" {
			writeJSONError(w, r, "No code snapshot found to deploy", http.StatusInternalServerError)
			return
		}

		newProc, err := s.orch.DeployWithEnv(name, code, proc.Env)
		if err != nil {
			writeJSONError(w, r, "Scale up failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		proc = newProc

		s.registerWithGateway(name, proc.Port)
	}

	targetURL, _ := url.Parse(fmt.Sprintf("http://localhost:%d", proc.Port))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.ServeHTTP(w, r)
}

func writeJSONError(w http.ResponseWriter, r *http.Request, msg string, status int) {
	var errorCode string
	switch status {
	case http.StatusMethodNotAllowed:
		errorCode = "ERR_METHOD_NOT_ALLOWED"
	case http.StatusBadRequest:
		errorCode = "ERR_BAD_REQUEST"
	case http.StatusUnauthorized:
		errorCode = "ERR_UNAUTHORIZED"
	case http.StatusForbidden:
		errorCode = "ERR_FORBIDDEN"
	case http.StatusNotFound:
		errorCode = "ERR_NOT_FOUND"
	case http.StatusConflict:
		errorCode = "ERR_CONFLICT"
	case http.StatusNotImplemented:
		errorCode = "ERR_NOT_IMPLEMENTED"
	default:
		errorCode = "ERR_INTERNAL_SERVER_ERROR"
	}
	ServShared.WriteJSONError(w, r, msg, errorCode, status)
}

