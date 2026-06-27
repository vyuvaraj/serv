package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"servcache/pkg/cache"
	"servcache/pkg/otel"

	"github.com/vyuvaraj/ServShared"
)

type Server struct {
	cache cache.Cache
	peers []string
}

func NewServer(c cache.Cache) *Server {
	var peers []string
	if rawPeers := os.Getenv("SERV_CACHE_PEERS"); rawPeers != "" {
		for _, p := range strings.Split(rawPeers, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				peers = append(peers, trimmed)
			}
		}
	}
	return &Server{
		cache: c,
		peers: peers,
	}
}

type SetRequest struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
	TTL   string      `json:"ttl,omitempty"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/readyz", ServShared.ReadyzHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/api/cache", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodPost:
			s.handleSet(w, req)
		case http.MethodDelete:
			s.handleClear(w, req)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/cache/", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 3 {
			http.Error(w, "Cache key required", http.StatusBadRequest)
			return
		}
		key := parts[2]

		switch req.Method {
		case http.MethodGet:
			s.handleGet(w, req, key)
		case http.MethodDelete:
			s.handleDelete(w, req, key)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	return ServShared.AuthMiddleware(mux)
}

func (s *Server) handleGet(w http.ResponseWriter, req *http.Request, key string) {
	traceparent := req.Header.Get("traceparent")
	span := otel.StartSpan(fmt.Sprintf("servcache:GET %s", key), traceparent)

	val, found, err := s.cache.Get(key)
	
	if span != nil {
		otel.EndSpan(span, err, map[string]interface{}{
			"cache.key":   key,
			"cache.hit":   found,
			"cache.error": err != nil,
		})
	}

	if err != nil {
		http.Error(w, "Cache Read Error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if !found {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":   key,
		"value": val,
	})
}

func (s *Server) handleSet(w http.ResponseWriter, req *http.Request) {
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Read Body Error", http.StatusBadRequest)
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var body SetRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Malformed JSON", http.StatusBadRequest)
		return
	}

	if body.Key == "" || body.Value == nil {
		http.Error(w, "Key and Value are required", http.StatusBadRequest)
		return
	}

	var ttl time.Duration
	if body.TTL != "" {
		parsed, err := time.ParseDuration(body.TTL)
		if err != nil {
			http.Error(w, "Invalid TTL format: "+err.Error(), http.StatusBadRequest)
			return
		}
		ttl = parsed
	}

	traceparent := req.Header.Get("traceparent")
	span := otel.StartSpan(fmt.Sprintf("servcache:SET %s", body.Key), traceparent)

	err = s.cache.Set(body.Key, body.Value, ttl)

	if span != nil {
		otel.EndSpan(span, err, map[string]interface{}{
			"cache.key":   body.Key,
			"cache.ttl":   body.TTL,
			"cache.error": err != nil,
		})
	}

	if err != nil {
		http.Error(w, "Cache Write Error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if req.URL.Query().Get("replicated") != "true" && len(s.peers) > 0 {
		s.replicate(http.MethodPost, "/api/cache", bodyBytes)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func (s *Server) handleDelete(w http.ResponseWriter, req *http.Request, key string) {
	traceparent := req.Header.Get("traceparent")
	span := otel.StartSpan(fmt.Sprintf("servcache:DELETE %s", key), traceparent)

	err := s.cache.Delete(key)

	if span != nil {
		otel.EndSpan(span, err, map[string]interface{}{
			"cache.key":   key,
			"cache.error": err != nil,
		})
	}

	if err != nil {
		http.Error(w, "Cache Delete Error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if req.URL.Query().Get("replicated") != "true" && len(s.peers) > 0 {
		s.replicate(http.MethodDelete, fmt.Sprintf("/api/cache/%s", key), nil)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func (s *Server) handleClear(w http.ResponseWriter, req *http.Request) {
	pattern := req.URL.Query().Get("pattern")
	
	var err error
	traceparent := req.Header.Get("traceparent")
	
	if pattern != "" {
		span := otel.StartSpan(fmt.Sprintf("servcache:DELETE_PATTERN %s", pattern), traceparent)
		err = s.cache.DeletePattern(pattern)
		if span != nil {
			otel.EndSpan(span, err, map[string]interface{}{
				"cache.pattern": pattern,
				"cache.error":   err != nil,
			})
		}
	} else {
		span := otel.StartSpan("servcache:CLEAR", traceparent)
		err = s.cache.Clear()
		if span != nil {
			otel.EndSpan(span, err, map[string]interface{}{
				"cache.error": err != nil,
			})
		}
	}

	if err != nil {
		http.Error(w, "Cache Clear/DeletePattern Error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if req.URL.Query().Get("replicated") != "true" && len(s.peers) > 0 {
		urlPath := "/api/cache"
		if pattern != "" {
			urlPath = fmt.Sprintf("/api/cache?pattern=%s", pattern)
		}
		s.replicate(http.MethodDelete, urlPath, nil)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func (s *Server) replicate(method string, path string, body []byte) {
	for _, peer := range s.peers {
		go func(p string) {
			url := fmt.Sprintf("%s%s", strings.TrimSuffix(p, "/"), path)
			if strings.Contains(path, "?") {
				url += "&replicated=true"
			} else {
				url += "?replicated=true"
			}
			var bodyReader io.Reader
			if body != nil {
				bodyReader = bytes.NewReader(body)
			}
			req, err := http.NewRequest(method, url, bodyReader)
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}(peer)
	}
}
