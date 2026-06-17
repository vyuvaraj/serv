package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	port       = flag.Int("port", 8083, "Port to listen on")
	gateUrl    = flag.String("gate-url", "http://localhost:8080", "ServGate base URL")
	storeUrl   = flag.String("store-url", "http://localhost:8081", "ServStore base URL")
	queueUrl   = flag.String("queue-url", "http://localhost:8082", "ServQueue base URL")
	authToken  = flag.String("auth-token", "gateway-secret-token", "Default API Auth token to use for downstream proxying")
	gateConfig = flag.String("gate-config", "../ServGate/config.json", "Path to ServGate config.json")
)

type Route struct {
	Prefix             string   `json:"prefix"`
	Target             string   `json:"target"`
	Targets            []string `json:"targets,omitempty"`
	LoadBalancer       string   `json:"load_balancer,omitempty"`
	TranspileType      string   `json:"transpile_type,omitempty"`
	Middleware         string   `json:"middleware,omitempty"`
	ResponseMiddleware string   `json:"response_middleware,omitempty"`
	RateLimitRPM       int      `json:"rate_limit_rpm,omitempty"`
	PromptGuard        bool     `json:"prompt_guard,omitempty"`
	PiiRedact          bool     `json:"pii_redact,omitempty"`
	SemanticCache      bool     `json:"semantic_cache,omitempty"`
}

type GatewayConfig struct {
	Addr      string  `json:"addr"`
	AuthToken string  `json:"auth_token"`
	TlsCert   string  `json:"tls_cert"`
	TlsKey    string  `json:"tls_key"`
	Routes    []Route `json:"routes"`
}

type ComponentStatus struct {
	Name      string    `json:"name"`
	Online    bool      `json:"online"`
	Url       string    `json:"url"`
	LatencyMs int64     `json:"latency_ms,omitempty"`
	Details   any       `json:"details,omitempty"`
}

var configMu sync.Mutex

func main() {
	flag.Parse()

	// Parse downstream URLs
	gURL, err := url.Parse(*gateUrl)
	if err != nil {
		log.Fatalf("Invalid gate-url: %v", err)
	}
	sURL, err := url.Parse(*storeUrl)
	if err != nil {
		log.Fatalf("Invalid store-url: %v", err)
	}
	qURL, err := url.Parse(*queueUrl)
	if err != nil {
		log.Fatalf("Invalid queue-url: %v", err)
	}

	// Create reverse proxies
	gateProxy := httputil.NewSingleHostReverseProxy(gURL)
	storeProxy := httputil.NewSingleHostReverseProxy(sURL)
	queueProxy := httputil.NewSingleHostReverseProxy(qURL)

	// Adjust Director to rewrite request path and set Authorization headers
	configureProxyDirector(gateProxy, gURL, "/api/proxy/gate", *authToken)
	configureProxyDirector(storeProxy, sURL, "/api/proxy/store", "")
	configureProxyDirector(queueProxy, qURL, "/api/proxy/queue", "secret-token")

	mux := http.NewServeMux()

	// 1. ServConsole Status Aggregator & Routes API
	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/routes", handleRoutes)
	mux.HandleFunc("/api/cluster", handleCluster)
	mux.HandleFunc("/api/cluster/rebalance", handleRebalance)

	// 2. Proxies
	mux.Handle("/api/proxy/gate/", gateProxy)
	mux.Handle("/api/proxy/store/", storeProxy)
	mux.Handle("/api/proxy/queue/", queueProxy)

	// 3. Static File Server
	fileServer := http.FileServer(http.Dir("./web"))
	mux.Handle("/", fileServer)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Starting ServConsole on http://localhost%s...", addr)
	log.Printf("Proxying Gateways to %s", *gateUrl)
	log.Printf("Proxying Storage to %s", *storeUrl)
	log.Printf("Proxying Queues to %s", *queueUrl)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func configureProxyDirector(proxy *httputil.ReverseProxy, target *url.URL, prefix string, defaultToken string) {
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Rewrite Path: remove prefix
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		// Set Target Host
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		// Set Auth Header if not present
		if defaultToken != "" && req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", "Bearer "+defaultToken)
		}
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	statuses := []ComponentStatus{
		checkStatus("ServGate", *gateUrl, "/"),
		checkStatus("ServStore", *storeUrl, "/console/metrics"),
		checkStatus("ServQueue", *queueUrl, "/api/stats"),
	}

	json.NewEncoder(w).Encode(map[string]any{
		"timestamp":  time.Now().Format(time.RFC3339),
		"components": statuses,
	})
}

func checkStatus(name string, baseUrl string, healthPath string) ComponentStatus {
	client := http.Client{
		Timeout: 1 * time.Second,
	}

	reqUrl := fmt.Sprintf("%s%s", strings.TrimSuffix(baseUrl, "/"), healthPath)
	req, err := http.NewRequest("GET", reqUrl, nil)
	if err != nil {
		return ComponentStatus{Name: name, Online: false, Url: baseUrl}
	}

	// Propagate default credentials for internal check
	if name == "ServGate" {
		req.Header.Set("Authorization", "Bearer "+*authToken)
	} else if name == "ServQueue" {
		req.Header.Set("Authorization", "Bearer secret-token")
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return ComponentStatus{Name: name, Online: false, Url: baseUrl}
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		var details any
		bodyBytes, err := io.ReadAll(resp.Body)
		if err == nil && len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &details)
		}
		return ComponentStatus{
			Name:      name,
			Online:    true,
			Url:       baseUrl,
			LatencyMs: latency,
			Details:   details,
		}
	}

	return ComponentStatus{
		Name:      name,
		Online:    false,
		Url:       baseUrl,
		LatencyMs: latency,
	}
}

func handleRoutes(w http.ResponseWriter, r *http.Request) {
	configMu.Lock()
	defer configMu.Unlock()

	// Locate and check if config file exists
	path, err := filepath.Abs(*gateConfig)
	if err != nil {
		http.Error(w, "Invalid config path: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Read current routes
	var cfg GatewayConfig
	configFile, err := os.Open(path)
	if err == nil {
		defer configFile.Close()
		_ = json.NewDecoder(configFile).Decode(&cfg)
	} else if !os.IsNotExist(err) {
		http.Error(w, "Failed to read config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Method == http.MethodPost {
		var newRoute Route
		if err := json.NewDecoder(r.Body).Decode(&newRoute); err != nil {
			http.Error(w, "Invalid route payload", http.StatusBadRequest)
			return
		}

		// Add or update route prefix
		found := false
		for i, r := range cfg.Routes {
			if r.Prefix == newRoute.Prefix {
				cfg.Routes[i] = newRoute
				found = true
				break
			}
		}
		if !found {
			cfg.Routes = append(cfg.Routes, newRoute)
		}

		// Save config.json
		writeBytes, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			http.Error(w, "Marshal error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if err := os.WriteFile(path, writeBytes, 0644); err != nil {
			http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("Successfully updated ServGate config with route prefix: %s", newRoute.Prefix)
	}

	// Return current list of routes
	w.Header().Set("Content-Type", "application/json")
	if cfg.Routes == nil {
		cfg.Routes = []Route{}
	}
	json.NewEncoder(w).Encode(cfg.Routes)
}

// NodeHealth wraps cluster NodeInfo with derived replication-lag metrics.
type NodeHealth struct {
	NodeID        string `json:"node_id"`
	Address       string `json:"address"`
	Status        string `json:"status"`
	Region        string `json:"region"`
	LastSeenAgoMs int64  `json:"last_seen_ago_ms"`
	LagStatus     string `json:"lag_status"` // "healthy" | "warning" | "critical"
	Load          int64  `json:"load"`
}

type ClusterHealth struct {
	Nodes          []NodeHealth `json:"nodes"`
	OnlineCount    int          `json:"online_count"`
	OfflineCount   int          `json:"offline_count"`
	ErasureCoding  bool         `json:"erasure_coding"`
	DataShards     int          `json:"data_shards"`
	ParityShards   int          `json:"parity_shards"`
	ClusterHealthy bool         `json:"cluster_healthy"`
}

func handleCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	client := http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", strings.TrimSuffix(*storeUrl, "/")+"/console/cluster/status", nil)
	if err != nil {
		json.NewEncoder(w).Encode(ClusterHealth{})
		return
	}

	type rawNode struct {
		NodeID   string    `json:"node_id"`
		Address  string    `json:"address"`
		Status   string    `json:"status"`
		LastSeen time.Time `json:"last_seen"`
		Load     int64     `json:"load"`
		Region   string    `json:"region"`
	}

	resp, err := client.Do(req)
	if err != nil {
		json.NewEncoder(w).Encode(ClusterHealth{})
		return
	}
	defer resp.Body.Close()

	var rawNodes []rawNode
	if err := json.NewDecoder(resp.Body).Decode(&rawNodes); err != nil {
		json.NewEncoder(w).Encode(ClusterHealth{})
		return
	}

	now := time.Now()
	var nodes []NodeHealth
	online, offline := 0, 0

	for _, n := range rawNodes {
		lagMs := int64(0)
		lagStatus := "healthy"
		if !n.LastSeen.IsZero() {
			lagMs = now.Sub(n.LastSeen).Milliseconds()
			switch {
			case lagMs > 10000:
				lagStatus = "critical"
			case lagMs > 5000:
				lagStatus = "warning"
			}
		}
		if n.Status == "online" {
			online++
		} else {
			offline++
			lagStatus = "critical"
		}
		nodes = append(nodes, NodeHealth{
			NodeID:        n.NodeID,
			Address:       n.Address,
			Status:        n.Status,
			Region:        n.Region,
			LastSeenAgoMs: lagMs,
			LagStatus:     lagStatus,
			Load:          n.Load,
		})
	}

	json.NewEncoder(w).Encode(ClusterHealth{
		Nodes:          nodes,
		OnlineCount:    online,
		OfflineCount:   offline,
		ErasureCoding:  false,
		DataShards:     2,
		ParityShards:   1,
		ClusterHealthy: offline == 0 && len(nodes) > 0,
	})
}

func handleRebalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	client := http.Client{Timeout: 3 * time.Second}
	body := strings.NewReader(`{"source_node":{"node_id":"servconsole","address":"localhost:8083","status":"online"},"peers":{}}`)
	req, err := http.NewRequest("POST",
		strings.TrimSuffix(*storeUrl, "/")+"/console/cluster/gossip", body)
	if err != nil {
		http.Error(w, "Request build failed", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "ServStore unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	log.Printf("Rebalance gossip round triggered, ServStore responded: %d", resp.StatusCode)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rebalance_triggered"})
}

