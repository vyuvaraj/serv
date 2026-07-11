package dashboards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
	"servconsole/pkg/config"
)

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	TraceID   string    `json:"trace_id,omitempty"`
}

type SLOIndicator struct {
	ServiceID              string  `json:"serviceId"`
	Name                   string  `json:"name"`
	TargetPercent          float64 `json:"targetPercent"`
	ActualPercent          float64 `json:"actualPercent"`
	BudgetRemainingPercent float64 `json:"budgetRemainingPercent"`
	TargetLatencyMs        int64   `json:"targetLatencyMs"`
	ActualLatencyMs        int64   `json:"actualLatencyMs"`
	Status                 string  `json:"status"` // healthy, warning, breached
	BurnRate               float64 `json:"burnRate"` // e.g. 1.0x, 4.2x
}

type CostMetrics struct {
	MonthlySpendUSD float64            `json:"monthlySpendUSD"`
	ForecastUSD     float64            `json:"forecastUSD"`
	SavingsUSD      float64            `json:"savingsUSD"`
	Breakdown       map[string]float64 `json:"breakdown"`
}

type DashboardWidget struct {
	ID        string `json:"id"`
	Title      string `json:"title"`
	Metric     string `json:"metric"`
	ChartType  string `json:"chart_type"`
	TimeRange  string `json:"time_range"`
	Service    string `json:"service"`
	PositionX  int    `json:"position_x"`
	PositionY  int    `json:"position_y"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

type Dashboard struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	CreatedBy   string            `json:"created_by"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
	Widgets     []DashboardWidget `json:"widgets"`
	SharedWith  []string          `json:"shared_with"`
}

type CapacityResponse struct {
	CPUUsagePct      float64 `json:"cpu_usage_pct"`
	MemoryUsagePct   float64 `json:"memory_usage_pct"`
	DiskUsagePct     float64 `json:"disk_usage_pct"`
	DaysToExhaust    int     `json:"days_to_exhaust"`
	ForecastAnalysis string  `json:"forecast_analysis"`
}

type CorrelationEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Source      string    `json:"source"`
	Severity    string    `json:"severity"`
}

var (
	LogBuffer   *[]LogEntry
	LogBufferMu *sync.Mutex

	Dashboards   *[]Dashboard
	DashboardsMu *sync.Mutex

	CheckStatus        func(string, string) config.ComponentStatus
	WriteJSONError     func(http.ResponseWriter, *http.Request, string, string, int)
	AddOrUpdateAlert   func(string, string, string, string)
	ClearAlert         func(string, string)
	GetUserRole        func(*http.Request) string
	HandleScaleTrigger func(string, string)
	AddAuditLog        func(user string, action string, method string, path string, status int)

	AlertsMuLock   func()
	AlertsMuUnlock func()
)

func Init(
	checkStatus func(string, string) config.ComponentStatus,
	writeError func(http.ResponseWriter, *http.Request, string, string, int),
	addAlert func(string, string, string, string),
	clearAlert func(string, string),
	getUserRole func(*http.Request) string,
	scaleTrigger func(string, string),
	auditLog func(string, string, string, string, int),
) {
	CheckStatus = checkStatus
	WriteJSONError = writeError
	AddOrUpdateAlert = addAlert
	ClearAlert = clearAlert
	GetUserRole = getUserRole
	HandleScaleTrigger = scaleTrigger
	AddAuditLog = auditLog
}

func HandleIngestLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var entry LogEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	LogBufferMu.Lock()
	*LogBuffer = append(*LogBuffer, entry)
	if len(*LogBuffer) > 2000 {
		*LogBuffer = (*LogBuffer)[1:]
	}
	LogBufferMu.Unlock()

	// Autocatalytic Self-Healing logic
	if entry.Level == "error" || entry.Level == "critical" {
		if strings.Contains(entry.Message, "connection pool exhausted") || strings.Contains(entry.Message, "too many open files") {
			if AlertsMuLock != nil && AlertsMuUnlock != nil {
				AlertsMuLock()
				if AddOrUpdateAlert != nil {
					AddOrUpdateAlert(entry.Service, "scaling_trigger", "High load detected in logs: "+entry.Message, "warning")
				}
				AlertsMuUnlock()
			}
			if HandleScaleTrigger != nil {
				go HandleScaleTrigger(entry.Service, entry.Message)
			}
		}
	}

	// AI Observability: Trigger automatic scaling when high load is detected
	if strings.Contains(entry.Message, "[HIGH_LOAD]") {
		cloudURL := os.Getenv("SERV_CLOUD_URL")
		if cloudURL == "" {
			cloudURL = "http://localhost:8085"
		}
		go func(serviceName string) {
			url := fmt.Sprintf("%s/api/services/%s/scale", strings.TrimSuffix(cloudURL, "/"), serviceName)
			payload := map[string]interface{}{"replicas": 3}
			body, _ := json.Marshal(payload)
			req, err := http.NewRequest("POST", url, bytes.NewReader(body))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
					svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
					if svcToken != "" {
						req.Header.Set("Authorization", "Bearer "+svcToken)
					}
				}
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}(entry.Service)

		if AlertsMuLock != nil && AlertsMuUnlock != nil {
			AlertsMuLock()
			if AddOrUpdateAlert != nil {
				AddOrUpdateAlert(entry.Service, "scaling_trigger", "Auto-scaled service due to high load log signature: "+entry.Message, "info")
			}
			AlertsMuUnlock()
		}
	}

	// AI Observability: Trigger query cache rule mutations when slow queries are detected
	if strings.Contains(entry.Message, "[SLOW_QUERY_ALERT]") || strings.Contains(entry.Message, "[DB_LATENCY_ALERT]") {
		go func(serviceName string) {
			config.ConfigMu.Lock()
			defer config.ConfigMu.Unlock()

			var prov config.ConfigProvider
			if os.Getenv("SERV_CONFIG_S3_BUCKET") != "" || os.Getenv("SERVVERSE_DISCOVERY") != "" {
				prov = config.NewS3ConfigProvider()
			} else {
				prov = config.NewLocalFileProvider(*config.GateConfig)
			}

			cfg, err := prov.Load()
			if err == nil && cfg != nil {
				mutated := false
				for i, route := range cfg.Routes {
					if strings.Contains(route.Target, serviceName) || strings.TrimPrefix(route.Prefix, "/") == serviceName {
						if !route.SemanticCache {
							cfg.Routes[i].SemanticCache = true
							mutated = true
						}
					}
				}
				if mutated {
					_ = prov.Save(cfg)
					if AddAuditLog != nil {
						AddAuditLog("system-ai", "AI Observability Mutation: Enabled semantic cache for "+serviceName+" due to query latency logs", "AUTO_MUTATE", "/api/logs/ingest", http.StatusOK)
					}
				}
			}
		}(entry.Service)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func HandleGetLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	svc := r.URL.Query().Get("service")
	level := r.URL.Query().Get("level")
	q := r.URL.Query().Get("q")

	LogBufferMu.Lock()
	defer LogBufferMu.Unlock()

	filtered := []LogEntry{}
	for _, entry := range *LogBuffer {
		if svc != "" && entry.Service != svc {
			continue
		}
		if level != "" && !strings.EqualFold(entry.Level, level) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(entry.Message), strings.ToLower(q)) {
			continue
		}
		filtered = append(filtered, entry)
	}

	json.NewEncoder(w).Encode(filtered)
}

func HandleSLO(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	slos := []SLOIndicator{
		{
			ServiceID:              "ServGate",
			Name:                   "Gateway Uptime (Success Rate)",
			TargetPercent:          99.9,
			ActualPercent:          99.95,
			BudgetRemainingPercent: 92.4,
			TargetLatencyMs:        150,
			ActualLatencyMs:        10,
			Status:                 "healthy",
			BurnRate:               1.0,
		},
		{
			ServiceID:              "ServStore",
			Name:                   "Storage Object Read Latency",
			TargetPercent:          99.5,
			ActualPercent:          99.62,
			BudgetRemainingPercent: 88.1,
			TargetLatencyMs:        200,
			ActualLatencyMs:        12,
			Status:                 "healthy",
			BurnRate:               1.1,
		},
		{
			ServiceID:              "ServQueue",
			Name:                   "Queue Message Dispatch Success",
			TargetPercent:          99.9,
			ActualPercent:          99.99,
			BudgetRemainingPercent: 95.0,
			TargetLatencyMs:        50,
			ActualLatencyMs:        5,
			Status:                 "healthy",
			BurnRate:               0.8,
		},
		{
			ServiceID:              "ServTunnel",
			Name:                   "Tunnel Tunnel Connection Stability",
			TargetPercent:          99.0,
			ActualPercent:          99.2,
			BudgetRemainingPercent: 80.0,
			TargetLatencyMs:        300,
			ActualLatencyMs:        45,
			Status:                 "healthy",
			BurnRate:               1.2,
		},
	}

	json.NewEncoder(w).Encode(slos)
}

func HandleCostEstimation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	gateStatus := CheckStatus("ServGate", *config.GateUrl)
	storeStatus := CheckStatus("ServStore", *config.StoreUrl)
	queueStatus := CheckStatus("ServQueue", *config.QueueUrl)

	var storageBytes int64 = 524288000
	var bucketsCount int64 = 3
	var gateRequests int64 = 150000
	var queueMessages int64 = 85000

	if storeStatus.Online && storeStatus.Details != nil {
		if m, ok := storeStatus.Details.(map[string]any); ok {
			if bytesVal, exists := m["TotalBytes"]; exists {
				if f, ok := bytesVal.(float64); ok {
					storageBytes = int64(f)
				}
			}
			if bktVal, exists := m["BucketsCount"]; exists {
				if f, ok := bktVal.(float64); ok {
					bucketsCount = int64(f)
				}
			}
		}
	}

	if gateStatus.Online && gateStatus.Details != nil {
		if m, ok := gateStatus.Details.(map[string]any); ok {
			if reqsVal, exists := m["requests_total"]; exists {
				if f, ok := reqsVal.(float64); ok {
					gateRequests = int64(f)
				}
			}
		}
	}

	if queueStatus.Online && queueStatus.Details != nil {
		if m, ok := queueStatus.Details.(map[string]any); ok {
			if metrics, ok := m["metrics"].(map[string]any); ok {
				if pubVal, exists := metrics["messages_published_total"]; exists {
					if f, ok := pubVal.(float64); ok {
						queueMessages = int64(f)
					}
				}
			}
		}
	}

	storageGB := float64(storageBytes) / (1024 * 1024 * 1024)
	storageCost := storageGB * 0.023
	if storageCost < 0.01 && storageBytes > 0 {
		storageCost = 0.01
	}

	gateCost := (float64(gateRequests) / 10000.0) * 0.005
	queueCost := (float64(queueMessages) / 1000000.0) * 0.05
	baselineCost := 20.0

	totalCost := storageCost + gateCost + queueCost + baselineCost
	budgetLimit := 50.0

	recommendations := []string{}
	if gateRequests > 500000 {
		recommendations = append(recommendations, "Enable route-level caching on ServGate to reduce CPU and baseline cost.")
	}
	if bucketsCount > 10 {
		recommendations = append(recommendations, "Consolidate unused S3 buckets to reduce storage index overhead.")
	}
	if storageGB > 100 {
		recommendations = append(recommendations, "Configure cold storage offloading policy in ServQueue to migrate historical logs to standard compression.")
	}
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Ecosystem resources are optimized. No actions required.")
	}

	response := map[string]any{
		"timestamp": time.Now().Format(time.RFC3339),
		"monthly": map[string]any{
			"total":   totalCost,
			"budget":  budgetLimit,
			"percent": (totalCost / budgetLimit) * 100,
		},
		"breakdown": []map[string]any{
			{"name": "Compute (Baseline)", "value": baselineCost, "color": "#6366f1"},
			{"name": "Storage (ServStore)", "value": storageCost, "color": "#10b981"},
			{"name": "Gateway (ServGate)", "value": gateCost, "color": "#06b6d4"},
			{"name": "Queue (ServQueue)", "value": queueCost, "color": "#f59e0b"},
		},
		"metrics": map[string]any{
			"storage_bytes":  storageBytes,
			"storage_gb":     storageGB,
			"gate_requests":  gateRequests,
			"queue_messages": queueMessages,
			"buckets_count":  bucketsCount,
		},
		"recommendations": recommendations,
	}

	json.NewEncoder(w).Encode(response)
}

func HandleDashboards(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		if DashboardsMu == nil || Dashboards == nil {
			json.NewEncoder(w).Encode([]Dashboard{})
			return
		}
		DashboardsMu.Lock()
		result := make([]Dashboard, len(*Dashboards))
		copy(result, *Dashboards)
		DashboardsMu.Unlock()
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var newDash Dashboard
		if err := json.NewDecoder(r.Body).Decode(&newDash); err != nil || newDash.Name == "" {
			WriteJSONError(w, r, "Invalid dashboard payload — name is required", "ERR_INVALID_BODY", http.StatusBadRequest)
			return
		}

		now := time.Now().Format(time.RFC3339)
		if newDash.ID == "" {
			newDash.ID = fmt.Sprintf("dash-%d", time.Now().UnixNano())
		}
		newDash.CreatedAt = now
		newDash.UpdatedAt = now
		if newDash.CreatedBy == "" {
			newDash.CreatedBy = "console-operator"
		}
		if newDash.Widgets == nil {
			newDash.Widgets = []DashboardWidget{}
		}
		if newDash.SharedWith == nil {
			newDash.SharedWith = []string{}
		}

		if DashboardsMu != nil && Dashboards != nil {
			DashboardsMu.Lock()
			*Dashboards = append(*Dashboards, newDash)
			DashboardsMu.Unlock()
		}

		if AddAuditLog != nil {
			AddAuditLog(newDash.CreatedBy, fmt.Sprintf("Created dashboard: %s", newDash.Name), r.Method, r.URL.Path, http.StatusCreated)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(newDash)

	case http.MethodPut:
		var updated Dashboard
		if err := json.NewDecoder(r.Body).Decode(&updated); err != nil || updated.ID == "" {
			WriteJSONError(w, r, "Invalid dashboard payload — id is required", "ERR_INVALID_BODY", http.StatusBadRequest)
			return
		}

		updated.UpdatedAt = time.Now().Format(time.RFC3339)
		if updated.Widgets == nil {
			updated.Widgets = []DashboardWidget{}
		}
		if updated.SharedWith == nil {
			updated.SharedWith = []string{}
		}

		found := false
		if DashboardsMu != nil && Dashboards != nil {
			DashboardsMu.Lock()
			for i, d := range *Dashboards {
				if d.ID == updated.ID {
					if updated.CreatedAt == "" {
						updated.CreatedAt = d.CreatedAt
					}
					if updated.CreatedBy == "" {
						updated.CreatedBy = d.CreatedBy
					}
					(*Dashboards)[i] = updated
					found = true
					break
				}
			}
			DashboardsMu.Unlock()
		}

		if !found {
			WriteJSONError(w, r, "Dashboard not found", "ERR_NOT_FOUND", http.StatusNotFound)
			return
		}

		if AddAuditLog != nil {
			AddAuditLog("console-operator", fmt.Sprintf("Updated dashboard: %s", updated.Name), r.Method, r.URL.Path, http.StatusOK)
		}
		json.NewEncoder(w).Encode(updated)

	case http.MethodDelete:
		dashID := r.URL.Query().Get("id")
		if dashID == "" {
			WriteJSONError(w, r, "Query parameter 'id' is required", "ERR_MISSING_ID", http.StatusBadRequest)
			return
		}

		found := false
		if DashboardsMu != nil && Dashboards != nil {
			DashboardsMu.Lock()
			for i, d := range *Dashboards {
				if d.ID == dashID {
					*Dashboards = append((*Dashboards)[:i], (*Dashboards)[i+1:]...)
					found = true
					break
				}
			}
			DashboardsMu.Unlock()
		}

		if !found {
			WriteJSONError(w, r, "Dashboard not found", "ERR_NOT_FOUND", http.StatusNotFound)
			return
		}

		if AddAuditLog != nil {
			AddAuditLog("console-operator", fmt.Sprintf("Deleted dashboard: %s", dashID), r.Method, r.URL.Path, http.StatusOK)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "deleted_id": dashID})

	default:
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
	}
}

func HandleCapacityPlanning(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	json.NewEncoder(w).Encode(CapacityResponse{
		CPUUsagePct:      42.5,
		MemoryUsagePct:   68.2,
		DiskUsagePct:     55.4,
		DaysToExhaust:    45,
		ForecastAnalysis: "Based on current storage consumption rate of 1.2 GB/day, disk capacity will be exhausted in approximately 45 days. Average CPU and Memory utilization remain stable. Recommending vertical scaling or archival rule configuration for ServStore within 30 days to mitigate exhaustion risk.",
	})
}

func HandleCorrelationTimeline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	events := []CorrelationEvent{
		{
			Timestamp:   time.Now().Add(-10 * time.Minute),
			Type:        "deployment",
			Title:       "Deployed ServGate v1.4.2",
			Description: "Changelog: Optimize JSON marshal in proxy pipelines.",
			Source:      "ServCloud",
			Severity:    "info",
		},
		{
			Timestamp:   time.Now().Add(-8 * time.Minute),
			Type:        "alert",
			Title:       "High Latency Alert: ServGate",
			Description: "p99 latency spiked to 312ms.",
			Source:      "ServConsole",
			Severity:    "warning",
		},
		{
			Timestamp:   time.Now().Add(-7 * time.Minute),
			Type:        "log",
			Title:       "Database Connection pool exhausted",
			Description: "Active connections reached 100 in ServDB pool.",
			Source:      "ServDB",
			Severity:    "error",
		},
	}

	json.NewEncoder(w).Encode(events)
}

type NLQResult struct {
	Query       string   `json:"query"`
	Interpreted string   `json:"interpreted"`
	TraceCount  int      `json:"traceCount"`
	ErrorCount  int      `json:"errorCount"`
	TotalSpans  int      `json:"totalSpans"`
	Services    []string `json:"services"`
	Summary     string   `json:"summary"`
}

type PredictiveAlert struct {
	ID         string  `json:"id"`
	Metric     string  `json:"metric"`
	Service    string  `json:"service"`
	Current    float64 `json:"current"`
	Threshold  float64 `json:"threshold"`
	Unit       string  `json:"unit"`
	DaysUntil  int     `json:"daysUntil"`
	Severity   string  `json:"severity"`
	Suggestion string  `json:"suggestion"`
}

func HandleNLQ(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		if strings.Contains(strings.ToLower(req.Prompt), "error") {
			json.NewEncoder(w).Encode(map[string]any{
				"sql":     "SELECT timestamp, service, level, message FROM logs WHERE level = 'error' ORDER BY timestamp DESC LIMIT 50;",
				"comment": "Identified natural language request for errors, generating matching SQL query.",
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{
				"sql":     "SELECT timestamp, service, level, message FROM logs ORDER BY timestamp DESC LIMIT 10;",
				"comment": "Fallback query to show the latest logs.",
			})
		}
		return
	}

	if r.Method == http.MethodGet {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteJSONError(w, r, "Missing query parameter 'q'", "ERR_MISSING_PARAM", http.StatusBadRequest)
			return
		}

		result := NLQResult{
			Query:       q,
			Interpreted: "Trace filter: service=ServDB, status=error, window=last 1 hour",
			TraceCount:  42,
			ErrorCount:  17,
			TotalSpans:  284,
			Services:    []string{"ServGate", "ServStore", "ServDB"},
			Summary:     "Found 42 traces touching ServDB in the last hour. 17 contained errors (40%). Peak error rate at 14:03 UTC (p99 latency: 620ms). Most common error: 'context deadline exceeded' on SELECT queries. Likely correlated with ServStore v1.4.2 deploy at 13:58 UTC.",
		}

		json.NewEncoder(w).Encode(result)
		return
	}

	WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
}

// PredictiveAlertsProvider defines pluggable hooks for querying predictive system alerts.
type PredictiveAlertsProvider interface {
	GetAlerts() []PredictiveAlert
}

// ActivePredictiveAlertsProvider is the globally registered predictive alerts provider hook.
var ActivePredictiveAlertsProvider PredictiveAlertsProvider

func HandlePredictiveAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	if ActivePredictiveAlertsProvider == nil {
		WriteJSONError(w, r, "Predictive alerts require Enterprise Edition", "ERR_EE_REQUIRED", http.StatusForbidden)
		return
	}

	alertsList := ActivePredictiveAlertsProvider.GetAlerts()
	json.NewEncoder(w).Encode(alertsList)
}
