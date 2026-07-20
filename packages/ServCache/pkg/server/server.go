package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"servcache/pkg/cache"
	"servcache/pkg/otel"

	"github.com/vyuvaraj/ServShared"
)

type Server struct {
	cache     cache.Cache
	peers     []string
	hits      uint64
	misses    uint64
	hotKeys   map[string]uint64
	hotKeysMu sync.Mutex
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
		cache:   c,
		peers:   peers,
		hotKeys: make(map[string]uint64),
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
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servcache", "1.0.0"))
	mux.HandleFunc("/api/v1/version", ServShared.VersionHandler("servcache", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		s.handlePrometheusMetrics(w, req)
	})

	mux.HandleFunc("/api/cache/gossip-invalidate", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			s.handleGossipInvalidate(w, req)
		} else {
			s.writeJSONError(w, req, "Method Not Allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/cache", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodPost:
			s.handleSet(w, req)
		case http.MethodDelete:
			s.handleClear(w, req)
		default:
			s.writeJSONError(w, req, "Method Not Allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/cache/", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 3 {
			s.writeJSONError(w, req, "Cache key required", "ERR_BAD_REQUEST", http.StatusBadRequest)
			return
		}
		key := parts[2]

		if key == "inspect" {
			s.handleInspect(w, req)
			return
		}

		switch req.Method {
		case http.MethodGet:
			s.handleGet(w, req, key)
		case http.MethodDelete:
			s.handleDelete(w, req, key)
		default:
			s.writeJSONError(w, req, "Method Not Allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		}
	})

	// Wrapper handler for /api/v1/ prefix rewriting (V1.1 support)
	v1Wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
		}
		mux.ServeHTTP(w, r)
	})

	// Wrap in ServShared middleware: RateLimit → CORS → MaxBytes → JWT auth → tenant enforcement → handlers
	return ServShared.RateLimitMiddleware(
		ServShared.CORSMiddleware(
			ServShared.MaxBytesMiddleware(10*1024*1024)(
				ServShared.AuthMiddleware(
					ServShared.TenantMiddleware(v1Wrapper),
				),
			),
		),
	)
}

func (s *Server) handleGet(w http.ResponseWriter, req *http.Request, key string) {
	key = s.isolateKey(req, key)
	traceparent := req.Header.Get("traceparent")
	span := otel.StartSpan(fmt.Sprintf("servcache:GET %s", key), traceparent)

	val, found, err := s.cache.Get(key)
	if err == nil {
		if found {
			atomic.AddUint64(&s.hits, 1)
		} else {
			atomic.AddUint64(&s.misses, 1)
		}
		s.trackHotKey(key)
	}
	
	if span != nil {
		otel.EndSpan(span, err, map[string]interface{}{
			"cache.key":   key,
			"cache.hit":   found,
			"cache.error": err != nil,
		})
	}

	if err != nil {
		s.writeJSONError(w, req, "Cache Read Error: "+err.Error(), "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
		return
	}

	if !found {
		s.writeJSONError(w, req, "Key not found", "ERR_NOT_FOUND", http.StatusNotFound)
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
		s.writeJSONError(w, req, "Read Body Error", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var body SetRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		s.writeJSONError(w, req, "Malformed JSON", "ERR_BAD_REQUEST_BODY", http.StatusBadRequest)
		return
	}

	if body.Key == "" || body.Value == nil {
		s.writeJSONError(w, req, "Key and Value are required", "ERR_BAD_REQUEST_BODY", http.StatusBadRequest)
		return
	}

	var ttl time.Duration
	if body.TTL != "" {
		parsed, err := time.ParseDuration(body.TTL)
		if err != nil {
			s.writeJSONError(w, req, "Invalid TTL format: "+err.Error(), "ERR_BAD_REQUEST_BODY", http.StatusBadRequest)
			return
		}
		ttl = parsed
	}

	body.Key = s.isolateKey(req, body.Key)
	bodyBytes, _ = json.Marshal(body)

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
		s.writeJSONError(w, req, "Cache Write Error: "+err.Error(), "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
		return
	}

	if req.URL.Query().Get("replicated") != "true" && len(s.peers) > 0 {
		s.replicate(http.MethodPost, "/api/cache", bodyBytes)
		s.gossipInvalidate(body.Key, "")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func (s *Server) handleDelete(w http.ResponseWriter, req *http.Request, key string) {
	key = s.isolateKey(req, key)
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
		s.writeJSONError(w, req, "Cache Delete Error: "+err.Error(), "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
		return
	}

	if req.URL.Query().Get("replicated") != "true" && len(s.peers) > 0 {
		s.replicate(http.MethodDelete, fmt.Sprintf("/api/cache/%s", key), nil)
		s.gossipInvalidate(key, "")
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func (s *Server) handleClear(w http.ResponseWriter, req *http.Request) {
	pattern := req.URL.Query().Get("pattern")
	
	var err error
	traceparent := req.Header.Get("traceparent")
	
	if pattern != "" {
		pattern = s.isolateKey(req, pattern)
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
		s.writeJSONError(w, req, "Cache Clear/DeletePattern Error: "+err.Error(), "ERR_INTERNAL_SERVER_ERROR", http.StatusInternalServerError)
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

func (s *Server) trackHotKey(key string) {
	s.hotKeysMu.Lock()
	defer s.hotKeysMu.Unlock()
	if s.hotKeys == nil {
		s.hotKeys = make(map[string]uint64)
	}
	s.hotKeys[key]++
}

func (s *Server) handleInspect(w http.ResponseWriter, _ *http.Request) {
	s.hotKeysMu.Lock()
	hot := make(map[string]uint64)
	for k, v := range s.hotKeys {
		hot[k] = v
	}
	s.hotKeysMu.Unlock()

	var totalKeys int
	namespaces := make(map[string]int)

	type keysLister interface {
		Keys() []string
	}
	if lister, ok := s.cache.(keysLister); ok {
		keys := lister.Keys()
		totalKeys = len(keys)
		for _, k := range keys {
			parts := strings.Split(k, ":")
			ns := "default"
			if len(parts) > 1 {
				ns = parts[0]
			}
			namespaces[ns]++
		}
	} else {
		totalKeys = len(hot)
		for k := range hot {
			parts := strings.Split(k, ":")
			ns := "default"
			if len(parts) > 1 {
				ns = parts[0]
			}
			namespaces[ns]++
		}
	}

	h := atomic.LoadUint64(&s.hits)
	m := atomic.LoadUint64(&s.misses)
	ratio := 0.0
	if h+m > 0 {
		ratio = float64(h) / float64(h+m)
	}

	resp := map[string]interface{}{
		"total_keys": totalKeys,
		"namespaces": namespaces,
		"hits":       h,
		"misses":     m,
		"hit_ratio":  ratio,
		"hot_keys":   hot,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	h := atomic.LoadUint64(&s.hits)
	m := atomic.LoadUint64(&s.misses)
	ratio := 0.0
	if h+m > 0 {
		ratio = float64(h) / float64(h+m)
	}

	var totalKeys int
	type keysLister interface {
		Keys() []string
	}
	if lister, ok := s.cache.(keysLister); ok {
		totalKeys = len(lister.Keys())
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP servcache_hits_total Total number of cache hits.\n")
	fmt.Fprintf(w, "# TYPE servcache_hits_total counter\n")
	fmt.Fprintf(w, "servcache_hits_total %d\n\n", h)

	fmt.Fprintf(w, "# HELP servcache_misses_total Total number of cache misses.\n")
	fmt.Fprintf(w, "# TYPE servcache_misses_total counter\n")
	fmt.Fprintf(w, "servcache_misses_total %d\n\n", m)

	fmt.Fprintf(w, "# HELP servcache_hit_ratio Current cache hit ratio (0.0 to 1.0).\n")
	fmt.Fprintf(w, "# TYPE servcache_hit_ratio gauge\n")
	fmt.Fprintf(w, "servcache_hit_ratio %f\n\n", ratio)

	fmt.Fprintf(w, "# HELP servcache_total_keys Current number of keys stored in cache.\n")
	fmt.Fprintf(w, "# TYPE servcache_total_keys gauge\n")
	fmt.Fprintf(w, "servcache_total_keys %d\n", totalKeys)
}

func (s *Server) handleGossipInvalidate(w http.ResponseWriter, req *http.Request) {
	key := req.URL.Query().Get("key")
	if key == "" {
		s.writeJSONError(w, req, "Key required", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}

	// Invalidate locally
	_ = s.cache.Delete(key)

	pathHeader := req.Header.Get("X-Gossip-Path")
	visited := make(map[string]bool)
	if pathHeader != "" {
		for _, v := range strings.Split(pathHeader, ",") {
			visited[v] = true
		}
	}

	s.gossipInvalidate(key, pathHeader)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"invalidated"}`))
}

func (s *Server) gossipInvalidate(key string, path string) {
	selfAddr := os.Getenv("SERV_CACHE_ADDR")
	if selfAddr == "" {
		selfAddr = "localhost:8083"
	}

	newPath := selfAddr
	if path != "" {
		newPath = path + "," + selfAddr
	}

	visited := make(map[string]bool)
	for _, p := range strings.Split(newPath, ",") {
		visited[p] = true
	}

	var candidates []string
	for _, p := range s.peers {
		if !visited[p] && p != selfAddr {
			candidates = append(candidates, p)
		}
	}

	if len(candidates) == 0 {
		return
	}

	fanout := 2
	if len(candidates) < fanout {
		fanout = len(candidates)
	}

	// Shuffle candidates to pick random peers
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	for i := 0; i < fanout; i++ {
		peer := candidates[i]
		go func(p string) {
			url := fmt.Sprintf("%s/api/cache/gossip-invalidate?key=%s&replicated=true", strings.TrimSuffix(p, "/"), key)
			req, err := http.NewRequest("POST", url, nil)
			if err != nil {
				return
			}
			req.Header.Set("X-Gossip-Path", newPath)

			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}(peer)
	}
}

func (s *Server) isolateKey(req *http.Request, key string) string {
	tid := ServShared.GetTenantID(req)
	if tid != "" && tid != "default" {
		return tid + ":" + key
	}
	return key
}

func (s *Server) writeJSONError(w http.ResponseWriter, r *http.Request, msg string, code string, status int) {
	ServShared.WriteJSONError(w, r, msg, code, status)
}
