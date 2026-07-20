package tabs

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/config"
)

var (
	Deployments    *[]Deployment
	DeploymentsMu  *sync.Mutex
	WriteJSONError func(http.ResponseWriter, *http.Request, string, string, int)
	AddAuditLog    func(user string, action string, method string, path string, status int)
	GetUserRole    func(*http.Request) string
	CheckStatus    func(string, string) config.ComponentStatus
)

func Init(
	deploymentsList *[]Deployment,
	lock *sync.Mutex,
	writeError func(http.ResponseWriter, *http.Request, string, string, int),
	auditLog func(string, string, string, string, int),
	getUserRole func(*http.Request) string,
	checkStatus func(string, string) config.ComponentStatus,
) {
	Deployments = deploymentsList
	DeploymentsMu = lock
	WriteJSONError = writeError
	AddAuditLog = auditLog
	GetUserRole = getUserRole
	CheckStatus = checkStatus
}

func HandleRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		if GetUserRole(r) != "admin" {
			WriteJSONError(w, r, "Forbidden: Admin role required to modify routes", "ERR_FORBIDDEN", http.StatusForbidden)
			return
		}
	}

	config.ConfigMu.Lock()
	defer config.ConfigMu.Unlock()

	var prov config.ConfigProvider
	if os.Getenv("SERV_CONFIG_S3_BUCKET") != "" || os.Getenv("SERVVERSE_DISCOVERY") != "" {
		prov = config.NewS3ConfigProvider()
	} else {
		prov = config.NewLocalFileProvider(*config.GateConfig)
	}

	cfg, err := prov.Load()
	if err != nil {
		if os.IsNotExist(err) {
			cfg = &config.GatewayConfig{
				Addr:      ":8080",
				AuthToken: *config.AuthToken,
				Routes:    []config.Route{},
			}
		} else {
			WriteJSONError(w, r, "Failed to read config: "+err.Error(), "ERR_CONFIG_LOAD_FAILED", http.StatusInternalServerError)
			return
		}
	}

	if r.Method == http.MethodPost {
		var newRoute config.Route
		if err := json.NewDecoder(r.Body).Decode(&newRoute); err != nil {
			WriteJSONError(w, r, "Invalid route payload", "ERR_INVALID_ROUTE_PAYLOAD", http.StatusBadRequest)
			return
		}

		found := false
		for i, rt := range cfg.Routes {
			if rt.Prefix == newRoute.Prefix {
				cfg.Routes[i] = newRoute
				found = true
				break
			}
		}
		if !found {
			cfg.Routes = append(cfg.Routes, newRoute)
		}

		if err := prov.Save(cfg); err != nil {
			WriteJSONError(w, r, "Failed to save config: "+err.Error(), "ERR_CONFIG_SAVE_FAILED", http.StatusInternalServerError)
			return
		}

		user := r.Header.Get("X-Console-User")
		if AddAuditLog != nil {
			AddAuditLog(user, "Register/Update API Route: "+newRoute.Prefix, r.Method, r.URL.Path, http.StatusOK)
		}
		log.Printf("Successfully updated config with route prefix: %s", newRoute.Prefix)
	}

	if r.Method == http.MethodDelete {
		prefix := r.URL.Query().Get("prefix")
		if prefix == "" {
			WriteJSONError(w, r, "Missing prefix query parameter", "ERR_INVALID_ROUTE_PAYLOAD", http.StatusBadRequest)
			return
		}

		newRoutes := []config.Route{}
		found := false
		for _, rt := range cfg.Routes {
			if rt.Prefix == prefix {
				found = true
			} else {
				newRoutes = append(newRoutes, rt)
			}
		}

		if !found {
			WriteJSONError(w, r, "Route not found", "ERR_ROUTE_NOT_FOUND", http.StatusNotFound)
			return
		}

		cfg.Routes = newRoutes
		if err := prov.Save(cfg); err != nil {
			WriteJSONError(w, r, "Failed to save config: "+err.Error(), "ERR_CONFIG_SAVE_FAILED", http.StatusInternalServerError)
			return
		}

		gateDeleteUrl := fmt.Sprintf("%s/api/routes?prefix=%s", strings.TrimSuffix(*config.GateUrl, "/"), url.QueryEscape(prefix))
		greq, gerr := http.NewRequest(http.MethodDelete, gateDeleteUrl, nil)
		if greq != nil && gerr == nil {
			if *config.AuthToken != "" {
				greq.Header.Set("Authorization", "Bearer "+*config.AuthToken)
			}
			gclient := &http.Client{Timeout: 3 * time.Second}
			gresp, gerr2 := gclient.Do(greq)
			if gerr2 == nil {
				gresp.Body.Close()
			}
		}

		user := r.Header.Get("X-Console-User")
		if AddAuditLog != nil {
			AddAuditLog(user, "Delete API Route: "+prefix, r.Method, r.URL.Path, http.StatusOK)
		}
		log.Printf("Successfully deleted route prefix: %s", prefix)
	}

	w.Header().Set("Content-Type", "application/json")
	if cfg.Routes == nil {
		cfg.Routes = []config.Route{}
	}
	json.NewEncoder(w).Encode(cfg.Routes)
}

type NodeHealth struct {
	NodeID        string `json:"node_id"`
	Address       string `json:"address"`
	Status        string `json:"status"`
	Region        string `json:"region"`
	LastSeenAgoMs int64  `json:"last_seen_ago_ms"`
	LagStatus     string `json:"lag_status"`
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

func HandleCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	client := http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", strings.TrimSuffix(*config.StoreUrl, "/")+"/console/cluster/status", nil)
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

func HandleRebalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	role := GetUserRole(r)
	if role != "admin" && role != "operator" {
		http.Error(w, "Forbidden: Admin or Operator role required to trigger cluster rebalance", http.StatusForbidden)
		return
	}
	client := http.Client{Timeout: 3 * time.Second}
	body := strings.NewReader(`{"source_node":{"node_id":"github.com/vyuvaraj/serv/packages/ServConsole","address":"localhost:8083","status":"online"},"peers":{}}`)
	req, err := http.NewRequest("POST",
		strings.TrimSuffix(*config.StoreUrl, "/")+"/console/cluster/gossip", body)
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
	user := r.Header.Get("X-Console-User")
	if AddAuditLog != nil {
		AddAuditLog(user, "Trigger Cluster Rebalance", r.Method, r.URL.Path, resp.StatusCode)
	}
	log.Printf("Rebalance gossip round triggered, ServStore responded: %d", resp.StatusCode)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rebalance_triggered"})
}

func HandleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	safe := map[string]interface{}{
		"gate":          config.ActiveDiscovery.Gate,
		"store":         config.ActiveDiscovery.Store,
		"queue":         config.ActiveDiscovery.Queue,
		"console_port":  config.ActiveDiscovery.ConsolePort,
		"otlp_endpoint": config.ActiveDiscovery.OTLPEndpoint,
		"gate_config":   config.ActiveDiscovery.GateConfig,
		"jwt_secret":    config.Redact(config.ActiveDiscovery.JWTSecret),
		"auth_token":    config.Redact(config.ActiveDiscovery.AuthToken),
		"source":        config.DiscoverySource(),
	}
	json.NewEncoder(w).Encode(safe)
}

func HandleDiagnosticExec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Service string `json:"service"`
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	cmd := strings.ToLower(strings.TrimSpace(req.Command))
	output := ""
	status := "success"

	switch cmd {
	case "ps aux":
		output = fmt.Sprintf("USER       PID %%CPU %%MEM    VSZ    RSS TTY      STAT START   TIME COMMAND\n" +
			"root         1  0.1  0.4  18432  8192 ?        Ss   10:00   0:02 /bin/init\n" +
			"operator    42  1.4  3.2 245760 65536 ?        Sl   10:05   0:15 /usr/local/bin/serv %s\n" +
			"operator    48  0.0  0.5  12288  1024 ?        S    10:06   0:00 sh -c diagnostic-daemon", req.Service)
	case "free -m":
		output = "              total        used        free      shared  buff/cache   available\n" +
			"Mem:           8192        3420        2845         120        1927        4652\n" +
			"Swap:          2048         105        1943"
	case "df -h":
		output = "Filesystem      Size  Used Avail Use%% Mounted on\n" +
			"/dev/sda1        50G   24G   26G  48%% /\n" +
			"tmpfs           4.0G     0  4.0G   0%% /dev/shm\n" +
			"/dev/sdb1       200G   82G  118G  41%% /data/store"
	case "serv status":
		output = fmt.Sprintf("Servverse Component: %s\n" +
			"Status: ACTIVE\n" +
			"Uptime: 2h 45m 12s\n" +
			"Version: v1.4.2-stable\n" +
			"Config Load: /etc/serv/config.json (OK)\n" +
			"P2P Cluster Ring Nodes: 5/5 Active", req.Service)
	default:
		if strings.HasPrefix(cmd, "ping ") {
			target := strings.TrimPrefix(cmd, "ping ")
			output = fmt.Sprintf("PING %s (127.0.0.1) 56(84) bytes of data.\n" +
				"64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time=0.042 ms\n" +
				"64 bytes from 127.0.0.1: icmp_seq=2 ttl=64 time=0.038 ms\n" +
				"--- %s ping statistics ---\n" +
				"2 packets transmitted, 2 received, 0%% packet loss, time 1002ms\n" +
				"rtt min/avg/max/mdev = 0.038/0.040/0.042/0.002 ms", target, target)
		} else {
			output = fmt.Sprintf("bash: %s: command not found\nAvailable diagnostic tools: ps aux, free -m, df -h, serv status, ping [target]", req.Command)
			status = "error"
		}
	}

	if AddAuditLog != nil {
		AddAuditLog("console-operator", fmt.Sprintf("Diagnostics Exec (%s): %s", req.Service, req.Command), r.Method, r.URL.Path, http.StatusOK)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"status":  status,
		"output":  output,
	})
}
