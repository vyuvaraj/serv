package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"servcron/pkg/cron"

	"github.com/vyuvaraj/ServShared"
)

var (
	EnterpriseVerifyRBAC = func(r *http.Request) bool { return true }
)

type Server struct {
	scheduler *cron.Scheduler
	elector   cron.LeaderElectionProvider
}

func NewServer(scheduler *cron.Scheduler, elector cron.LeaderElectionProvider) *Server {
	return &Server{
		scheduler: scheduler,
		elector:   elector,
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/readyz", ServShared.ReadyzHandler)
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servcron", "1.0.0"))
	mux.HandleFunc("/health", s.handleHealth)

	mux.HandleFunc("/api/jobs", s.handleJobsCollection)
	mux.HandleFunc("/api/v1/jobs", s.handleJobsCollection)
	mux.HandleFunc("/api/cron/smart-schedule", s.handleSmartSchedule)
	mux.HandleFunc("/api/v1/cron/smart-schedule", s.handleSmartSchedule)

	// Since we are using standard ServeMux, we can parse job IDs from path suffix
	mux.HandleFunc("/api/jobs/", s.handleJobsItem)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobsItem)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "follower"
	if s.elector.IsLeader() {
		status = "leader"
	}
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"role":   status,
	})
}

func (s *Server) handleJobsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listJobs(w, r)
	case http.MethodPost:
		s.createJob(w, r)
	default:
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleJobsItem(w http.ResponseWriter, r *http.Request) {
	// Parse ID and command
	// Path: /api/jobs/{id} or /api/jobs/{id}/run
	var path string
	if strings.HasPrefix(r.URL.Path, "/api/v1/jobs/") {
		path = strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	} else {
		path = strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	}

	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		WriteJSONError(w, r, "Missing job ID", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}

	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch r.Method {
	case http.MethodDelete:
		if action != "" {
			WriteJSONError(w, r, "Invalid path", "ERR_BAD_REQUEST", http.StatusBadRequest)
			return
		}
		s.deleteJob(w, r, id)
	case http.MethodPost:
		if action == "run" {
			s.triggerJob(w, r, id)
		} else {
			WriteJSONError(w, r, "Invalid action", "ERR_BAD_REQUEST", http.StatusBadRequest)
		}
	default:
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listJobs(w http.ResponseWriter, _ *http.Request) {
	jobs := s.scheduler.GetJobs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

func (s *Server) handleSmartSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}
	suggestions := s.scheduler.AnalyzeSchedules()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	if !EnterpriseVerifyRBAC(r) {
		WriteJSONError(w, r, "Forbidden: insufficient permissions", "ERR_FORBIDDEN", http.StatusForbidden)
		return
	}
	var job cron.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_BAD_REQUEST_BODY", http.StatusBadRequest)
		return
	}

	if err := s.scheduler.AddJob(&job); err != nil {
		WriteJSONError(w, r, err.Error(), "ERR_ADD_JOB_FAILED", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(job)
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request, id string) {
	if !EnterpriseVerifyRBAC(r) {
		WriteJSONError(w, r, "Forbidden: insufficient permissions", "ERR_FORBIDDEN", http.StatusForbidden)
		return
	}
	if !s.scheduler.RemoveJob(id) {
		WriteJSONError(w, r, "Job not found", "ERR_JOB_NOT_FOUND", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (s *Server) triggerJob(w http.ResponseWriter, r *http.Request, id string) {
	if !EnterpriseVerifyRBAC(r) {
		WriteJSONError(w, r, "Forbidden: insufficient permissions", "ERR_FORBIDDEN", http.StatusForbidden)
		return
	}
	if err := s.scheduler.TriggerJob(id); err != nil {
		WriteJSONError(w, r, err.Error(), "ERR_TRIGGER_JOB_FAILED", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	TraceID string `json:"trace_id,omitempty"`
}

func WriteJSONError(w http.ResponseWriter, r *http.Request, msg string, code string, status int) {
	traceID := ""
	if r != nil {
		traceparent := r.Header.Get("traceparent")
		if traceparent != "" {
			parts := strings.Split(traceparent, "-")
			if len(parts) >= 2 {
				traceID = parts[1]
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIError{
		Error:   msg,
		Code:    code,
		TraceID: traceID,
	})
}
