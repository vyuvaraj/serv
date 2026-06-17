package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"servqueue/pkg/broker"
)

type Server struct {
	addr   string
	engine *broker.BrokerEngine
}

func NewServer(addr string, engine *broker.BrokerEngine) *Server {
	return &Server{
		addr:   addr,
		engine: engine,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/topics/", s.handleRegisterTransform)
	mux.HandleFunc("/api/publish", s.handlePublish)
	mux.HandleFunc("/api/stats", s.handleStats)

	return http.ListenAndServe(s.addr, mux)
}

func (s *Server) handleRegisterTransform(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path. Use /api/topics/{topic}/transform", http.StatusBadRequest)
		return
	}
	topic := parts[3]

	wasmBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(wasmBytes) == 0 {
		s.engine.RegisterTransform(topic, nil)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("WASM transform cleared for topic " + topic))
		return
	}

	s.engine.RegisterTransform(topic, wasmBytes)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("WASM transform registered for topic " + topic))
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Topic   string `json:"topic"`
		Payload string `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	res, err := s.engine.Publish(r.Context(), req.Topic, req.Payload)
	if err != nil {
		http.Error(w, "Transform error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "delivered_payload": res})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "healthy",
	})
}
