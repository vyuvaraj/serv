package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/sijms/go-ora/v2"

	"github.com/vyuvaraj/serv/packages/ServShared"
	pkgalerts "github.com/vyuvaraj/serv/packages/ServConsole/pkg/alerts"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/config"
	pkgdashboards "github.com/vyuvaraj/serv/packages/ServConsole/pkg/dashboards"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/launcher"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/provision"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/proxy"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/tabs"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/topology"
	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/ws"
)

//go:embed web/*
var webAssets embed.FS

var (
	EnterpriseRegisterSession = func(token string, username string) error { return nil }
	EnterpriseVerifySession   = func(token string) bool { return true }
	EnterpriseRevokeSession   = func(token string) error { return nil }
)

type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	User      string    `json:"user"`
	Action    string    `json:"action"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
}

var (
	auditLogs   []AuditEntry
	auditLogsMu sync.Mutex
	auditFile   = "audit.json"
)

// Global state slices and mutexes to preserve package main references (e.g. for EE tag files)
var (
	alertsMu   sync.Mutex
	alerts     = []pkgalerts.Alert{}

	deploymentsMu sync.Mutex
	deployments = []tabs.Deployment{
		{ID: "dep-1", Version: "v1.4.1", Timestamp: time.Now().Add(-2 * time.Hour), Author: "vyuvaraj", Status: "historical", Changelog: "Initial stable build with local tracing and reverse proxy router config"},
		{ID: "dep-2", Version: "v1.4.2", Timestamp: time.Now().Add(-15 * time.Minute), Author: "vyuvaraj", Status: "active", Changelog: "Fix memory leak in tracing spans buffer and add live metrics websocket timeline"},
	}

	logBufferMu   sync.Mutex
	logBuffer     = []pkgdashboards.LogEntry{}

	dashboardsMu sync.Mutex
	dashboards   = []pkgdashboards.Dashboard{
		{
			ID:          "default-overview",
			Name:        "Ecosystem Overview",
			Description: "Default overview dashboard for all Servverse components",
			CreatedBy:   "system",
			CreatedAt:   "2026-06-01T00:00:00Z",
			UpdatedAt:   "2026-06-01T00:00:00Z",
			Widgets: []pkgdashboards.DashboardWidget{
				{ID: "w1", Title: "Gateway Latency", Metric: "latency", ChartType: "line", TimeRange: "1h", Service: "github.com/vyuvaraj/serv/packages/ServGate", PositionX: 0, PositionY: 0, Width: 6, Height: 4},
				{ID: "w2", Title: "Queue Throughput", Metric: "throughput", ChartType: "bar", TimeRange: "1h", Service: "github.com/vyuvaraj/serv/packages/ServQueue", PositionX: 6, PositionY: 0, Width: 6, Height: 4},
				{ID: "w3", Title: "Storage Error Rate", Metric: "error_rate", ChartType: "gauge", TimeRange: "6h", Service: "github.com/vyuvaraj/serv/packages/ServStore", PositionX: 0, PositionY: 4, Width: 4, Height: 3},
				{ID: "w4", Title: "Active Connections", Metric: "connections", ChartType: "line", TimeRange: "24h", Service: "github.com/vyuvaraj/serv/packages/ServTunnel", PositionX: 4, PositionY: 4, Width: 4, Height: 3},
				{ID: "w5", Title: "Service Health", Metric: "health", ChartType: "table", TimeRange: "1h", Service: "all", PositionX: 8, PositionY: 4, Width: 4, Height: 3},
			},
			SharedWith: []string{"platform-team", "sre-team"},
		},
	}
)

func main() {
	flag.Parse()

	config.ActiveDiscovery = config.LoadDiscovery()

	auth.EnterpriseRegisterSession = EnterpriseRegisterSession
	auth.EnterpriseVerifySession = EnterpriseVerifySession
	auth.EnterpriseRevokeSession = EnterpriseRevokeSession

	auth.Init(addAuditLog)
	pkgalerts.Init(&alerts, &alertsMu, config.CheckStatus, WriteJSONError)
	pkgdashboards.Init(config.CheckStatus, WriteJSONError, pkgalerts.AddOrUpdateAlert, pkgalerts.ClearAlert, auth.GetUserRole, handleScaleTrigger, addAuditLog)
	tabs.Init(&deployments, &deploymentsMu, WriteJSONError, addAuditLog, auth.GetUserRole, config.CheckStatus)
	topology.Init(WriteJSONError)
	provision.Init(WriteJSONError, addAuditLog)

	pkgdashboards.LogBuffer = &logBuffer
	pkgdashboards.LogBufferMu = &logBufferMu

	pkgdashboards.Dashboards = &dashboards
	pkgdashboards.DashboardsMu = &dashboardsMu

	pkgdashboards.AlertsMuLock = func() { alertsMu.Lock() }
	pkgdashboards.AlertsMuUnlock = func() { alertsMu.Unlock() }

	loadAuditLogs()
	tabs.LoadMigrations()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pkgalerts.StartAlertMonitoring(ctx)
	go startEventBroadcaster(ctx)

	if *config.StartAll {
		go launcher.OrchestrateStartup()
	}

	mux := http.NewServeMux()

	// Register Tab & Admin Downstream Handlers
	mux.HandleFunc("/api/status", config.HandleStatus)
	mux.HandleFunc("/api/routes", tabs.HandleRoutes)
	mux.HandleFunc("/api/cluster", tabs.HandleCluster)
	mux.HandleFunc("/api/rebalance", tabs.HandleRebalance)
	mux.HandleFunc("/api/discovery", tabs.HandleDiscovery)
	mux.HandleFunc("/api/auth/config", auth.HandleAuthConfig)
	mux.HandleFunc("/api/auth/login", auth.HandleLogin)
	mux.HandleFunc("/api/auth/callback", auth.HandleCallback)
	mux.HandleFunc("/api/auth/logout", auth.HandleLogout)
	mux.HandleFunc("/api/auth/me", auth.HandleAuthMe)
	mux.HandleFunc("/api/audit-logs", handleGetAuditLogs)
	mux.HandleFunc("/api/db/query", tabs.HandleDbQuery)
	mux.HandleFunc("/api/events", handleEvents)
	mux.HandleFunc("/api/migrations", tabs.HandleMigrations)
	mux.HandleFunc("/api/topology", topology.HandleTopology)
	mux.HandleFunc("/api/topology/live", topology.HandleTopologyLive)
	mux.HandleFunc("/api/traces/replay", topology.HandleTraceReplay)
	mux.HandleFunc("/api/traces/waterfall", topology.HandleTraceWaterfall)
	mux.HandleFunc("/api/alerts", pkgalerts.HandleAlerts)
	mux.HandleFunc("/api/alerts/ack", pkgalerts.HandleAlertsAck)
	mux.HandleFunc("/api/incidents/postmortem", pkgalerts.HandlePostmortem)
	mux.HandleFunc("/api/message/flow", pkgdashboards.HandleMessageFlow)
	mux.HandleFunc("/api/logs/ingest", pkgdashboards.HandleIngestLog)
	mux.HandleFunc("/api/logs", pkgdashboards.HandleGetLogs)
	mux.HandleFunc("/api/slo", pkgdashboards.HandleSLO)
	mux.HandleFunc("/api/cost-estimation", pkgdashboards.HandleCostEstimation)
	mux.HandleFunc("/api/dashboards", pkgdashboards.HandleDashboards)
	mux.HandleFunc("/api/capacity-planning", pkgdashboards.HandleCapacityPlanning)
	mux.HandleFunc("/api/correlation-timeline", pkgdashboards.HandleCorrelationTimeline)
	mux.HandleFunc("/api/nlq", pkgdashboards.HandleNLQ)
	mux.HandleFunc("/api/predictive-alerts", pkgdashboards.HandlePredictiveAlerts)
	mux.HandleFunc("/api/deployments", tabs.HandleDeployments)
	mux.HandleFunc("/api/rollback", tabs.HandleRollback)
	mux.HandleFunc("/api/environments", tabs.HandleEnvironments)
	mux.HandleFunc("/api/environments/select", tabs.HandleSelectEnvironment)
	mux.HandleFunc("/api/runbooks", tabs.HandleRunbooks)
	mux.HandleFunc("/api/runbooks/execute", tabs.HandleExecuteRunbook)
	mux.HandleFunc("/api/provision/store", provision.HandleProvisionStore)
	mux.HandleFunc("/api/provision/queue", provision.HandleProvisionQueue)
	mux.HandleFunc("/api/diagnostics/exec", tabs.HandleDiagnosticExec)
	mux.HandleFunc("/api/dev/services", launcher.HandleDevServices)
	mux.HandleFunc("/api/dev/restart", launcher.HandleDevRestart)
	mux.HandleFunc("/api/playground/compile", tabs.HandlePlaygroundCompile)
	mux.HandleFunc("/api/tenant/switch", tabs.HandleTenantSwitch)
	mux.HandleFunc("/api/plugins", tabs.HandleGetPlugins)
	mux.HandleFunc("/api/plugins/register", tabs.HandleRegisterPlugin)
	mux.HandleFunc("/api/plugins/serve", tabs.HandleServePlugin)
	mux.HandleFunc("/api/cron", tabs.HandleConsoleCronJobs)
	mux.HandleFunc("/api/cron/", tabs.HandleConsoleCronJobsItem)
	mux.HandleFunc("/api/cache/stats", tabs.HandleConsoleCacheStats)
	mux.HandleFunc("/api/cache/clear", tabs.HandleConsoleCacheClear)
	mux.HandleFunc("/api/locks", tabs.HandleConsoleLocks)
	mux.HandleFunc("/api/secrets", tabs.HandleConsoleSecrets)
	mux.HandleFunc("/api/mesh/instances", tabs.HandleConsoleMeshInstances)
	mux.HandleFunc("/api/registry/packages", tabs.HandleConsoleRegistryPackages)
	mux.HandleFunc("/api/cloud/services", tabs.HandleConsoleCloudServices)
	mux.HandleFunc("/api/cloud/services/", tabs.HandleConsoleCloudServicesItem)
	mux.HandleFunc("/api/cloud/deploy", tabs.HandleConsoleCloudDeploy)
	mux.HandleFunc("/api/cloud/history", tabs.HandleConsoleCloudHistory)
	mux.HandleFunc("/api/docs/spec", tabs.HandleDocsSpec)
	mux.HandleFunc("/api/playbooks", tabs.HandlePlaybooks)
	mux.HandleFunc("/api/playbooks/execute", tabs.HandleExecutePlaybook)
	mux.HandleFunc("/api/designer/layout", tabs.HandleDesignerLayout)
	mux.HandleFunc("/api/designer/sync", tabs.HandleDesignerSync)
	mux.HandleFunc("/api/studio/projects", tabs.HandleStudioProjects)
	mux.HandleFunc("/api/studio/debug", tabs.HandleStudioDebug)

	// AI and Enterprise tags registration handlers
	registerAIHandlers(mux)
	registerEnterpriseHandlers(mux)

	// Proxy Downstream Mappings
	registerProxies(mux)

	mux.HandleFunc("/api/version", ServShared.VersionHandler("github.com/vyuvaraj/serv/packages/ServConsole", "1.0.0"))

	// Wrapper handler for /api/v1/ prefix rewriting (V1.1 support)
	v1Wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
		}
		mux.ServeHTTP(w, r)
	})

	// Static Web Assets
	subFS, err := fs.Sub(webAssets, "web")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(subFS)))
	}

	// Middlewares chain: Trace -> RateLimit -> CORS -> MaxBytes -> v1Wrapper
	handlerChain := ServShared.TraceMiddleware("github.com/vyuvaraj/serv/packages/ServConsole",
		ServShared.RateLimitMiddleware(
			ServShared.CORSMiddleware(
				ServShared.MaxBytesMiddleware(10*1024*1024)(v1Wrapper),
			),
		),
	)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.ActiveDiscovery.ConsolePort),
		Handler: handlerChain,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[console] ServConsole running on http://localhost:%d", config.ActiveDiscovery.ConsolePort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[console] Listen failed: %v", err)
		}
	}()

	<-stop
	log.Println("[console] Shutting down ServConsole gracefully...")
	ctxShut, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShut()
	_ = server.Shutdown(ctxShut)
	log.Println("[console] ServConsole stopped.")
}

func registerProxies(mux *http.ServeMux) {
	proxies := []struct {
		prefix string
		url    string
	}{
		{"/proxy/gate", config.ActiveDiscovery.Gate},
		{"/proxy/store", config.ActiveDiscovery.Store},
		{"/proxy/queue", config.ActiveDiscovery.Queue},
		{"/proxy/trace", config.ActiveDiscovery.Trace},
		{"/proxy/tunnel", config.ActiveDiscovery.Tunnel},
		{"/proxy/auth", config.ActiveDiscovery.Auth},
		{"/proxy/db", config.ActiveDiscovery.DB},
		{"/proxy/mail", config.ActiveDiscovery.Mail},
		{"/proxy/flow", config.ActiveDiscovery.Flow},
		{"/proxy/mesh", config.ActiveDiscovery.Mesh},
		{"/proxy/cron", config.ActiveDiscovery.Cron},
		{"/proxy/cache", config.ActiveDiscovery.Cache},
		{"/proxy/registry", config.ActiveDiscovery.Registry},
		{"/proxy/cloud", config.ActiveDiscovery.Cloud},
		{"/proxy/docs", config.ActiveDiscovery.Docs},
		{"/proxy/lock", config.ActiveDiscovery.Lock},
	}

	for _, p := range proxies {
		if p.url == "" {
			continue
		}
		tgt, err := url.Parse(p.url)
		if err != nil {
			continue
		}
		rp := httputil.NewSingleHostReverseProxy(tgt)
		proxy.ConfigureProxyDirector(rp, tgt, p.prefix, config.ActiveDiscovery.AuthToken, getProxyActionName, addAuditLog)
		mux.Handle(p.prefix+"/", auth.AuthorizeConsole(rp.ServeHTTP))
	}
}

func WriteJSONError(w http.ResponseWriter, r *http.Request, msg string, code string, status int) {
	ServShared.WriteJSONError(w, r, msg, code, status)
}

func loadAuditLogs() {
	auditLogsMu.Lock()
	defer auditLogsMu.Unlock()
	data, err := os.ReadFile(auditFile)
	if err == nil {
		_ = json.Unmarshal(data, &auditLogs)
	}
	if auditLogs == nil {
		auditLogs = []AuditEntry{}
	}
}

func saveAuditLogs() {
	data, err := json.MarshalIndent(auditLogs, "", "  ")
	if err == nil {
		_ = os.WriteFile(auditFile, data, 0644)
	}
}

func addAuditLog(user string, action string, method string, path string, status int) {
	auditLogsMu.Lock()
	defer auditLogsMu.Unlock()

	if user == "" {
		user = "anonymous"
	}
	entry := AuditEntry{
		Timestamp: time.Now(),
		User:      user,
		Action:    action,
		Method:    method,
		Path:      path,
		Status:    status,
	}
	auditLogs = append([]AuditEntry{entry}, auditLogs...)
	if len(auditLogs) > 500 {
		auditLogs = auditLogs[:500]
	}
	saveAuditLogs()

	evt := map[string]any{
		"type":      "audit_log",
		"timestamp": entry.Timestamp,
		"user":      entry.User,
		"action":    entry.Action,
		"method":    entry.Method,
		"path":      entry.Path,
		"status":    entry.Status,
	}
	data, err := json.Marshal(evt)
	if err == nil {
		wsEventBroadcaster.Broadcast(string(data))
	}
}

func handleGetAuditLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	auditLogsMu.Lock()
	defer auditLogsMu.Unlock()
	json.NewEncoder(w).Encode(auditLogs)
}

func getProxyActionName(prefix string, path string) string {
	svc := strings.TrimPrefix(prefix, "/proxy/")
	switch svc {
	case "store":
		return "Object Storage Proxy Request"
	case "queue":
		return "Queue Broker Proxy Request"
	default:
		return "Proxy Request"
	}
}

var wsEventBroadcaster = ws.NewEventBroadcaster()

func startEventBroadcaster(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			evt := map[string]any{
				"type":      "system_metric",
				"timestamp": time.Now(),
				"metrics": map[string]any{
					"cpu_utilization": 22.4 + float64(time.Now().Unix()%10)*1.5,
					"memory_used_mb":  1204 + (time.Now().Unix()%20)*4,
					"active_sockets":  142 + (time.Now().Unix()%5),
				},
			}
			data, err := json.Marshal(evt)
			if err == nil {
				wsEventBroadcaster.Broadcast(string(data))
			}
		}
	}
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	wsEventBroadcaster.HandleEvents(w, r)
}

func handleScaleTrigger(service string, message string) {
	addAuditLog("system-autoscaler", "Auto-scaled service: "+service, "POST", "/api/logs/ingest", http.StatusOK)
}

func authorizeConsole(next http.HandlerFunc) http.HandlerFunc {
	return auth.AuthorizeConsole(next)
}
