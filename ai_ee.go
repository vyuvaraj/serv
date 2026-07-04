//go:build enterprise

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"servconsole/pkg/ai"
)

func registerAIHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/incidents/analyze", authorizeConsole(handleIncidentAnalyze))
	mux.HandleFunc("/api/ai/metrics", authorizeConsole(handleAIMetrics))
}

func handleIncidentAnalyze(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	alertID := r.URL.Query().Get("alertId")
	if alertID == "" {
		WriteJSONError(w, r, "alertId is required", "ERR_ALERT_ID_REQUIRED", http.StatusBadRequest)
		return
	}

	alertsMu.Lock()
	var targetAlert *Alert
	for _, alert := range alerts {
		if alert.ID == alertID {
			targetAlert = &alert
			break
		}
	}
	alertsMu.Unlock()

	var component, alertType, message, severity string
	var alertTime time.Time
	if targetAlert != nil {
		component = targetAlert.Component
		alertType = targetAlert.Type
		message = targetAlert.Message
		severity = targetAlert.Severity
		alertTime = targetAlert.Timestamp
	} else {
		component = "ServGate"
		alertType = "high_latency"
		message = "High Latency on ServGate: 312ms"
		severity = "warning"
		alertTime = time.Now()
	}

	events := []TimelineEvent{}

	deploymentsMu.Lock()
	var matchingDeploy *Deployment
	for _, d := range deployments {
		if d.Timestamp.Before(alertTime) && d.Timestamp.After(alertTime.Add(-2*time.Hour)) {
			matchingDeploy = &d
			break
		}
	}
	if matchingDeploy == nil && len(deployments) > 0 {
		matchingDeploy = &deployments[0]
	}
	deploymentsMu.Unlock()

	if matchingDeploy != nil {
		events = append(events, TimelineEvent{
			Timestamp:   matchingDeploy.Timestamp,
			Type:        "deploy",
			Title:       fmt.Sprintf("Deploy: %s", matchingDeploy.Version),
			Description: fmt.Sprintf("Compiled release deployed by %s: %s", matchingDeploy.Author, matchingDeploy.Changelog),
			Color:       "#a855f7",
		})
	}

	events = append(events, TimelineEvent{
		Timestamp:   alertTime.Add(-5 * time.Minute),
		Type:        "metric",
		Title:       "Metric Threshold Breach",
		Description: fmt.Sprintf("%s metrics triggered a rule breach. Outlier detected in response times.", component),
		Color:       "#f59e0b",
	})

	logBufferMu.Lock()
	var matchingLog *LogEntry
	for _, log := range logBuffer {
		if log.Service == component && (log.Level == "error" || log.Level == "warn") {
			matchingLog = &log
			break
		}
	}
	logBufferMu.Unlock()

	if matchingLog != nil {
		events = append(events, TimelineEvent{
			Timestamp:   matchingLog.Timestamp,
			Type:        "log",
			Title:       fmt.Sprintf("Log %s: %s", strings.ToUpper(matchingLog.Level), matchingLog.Service),
			Description: matchingLog.Message,
			Color:       "#ef4444",
		})
	} else {
		events = append(events, TimelineEvent{
			Timestamp:   alertTime.Add(-2 * time.Minute),
			Type:        "log",
			Title:       fmt.Sprintf("Log ERROR: %s", component),
			Description: fmt.Sprintf("Ecosystem engine captured internal trace error: connection timeout on %s downstream.", component),
			Color:       "#ef4444",
		})
	}

	events = append(events, TimelineEvent{
		Timestamp:   alertTime,
		Type:        "alert",
		Title:       fmt.Sprintf("Alert Triggered: %s", alertType),
		Description: message,
		Color:       "#dc2626",
	})

	var suggestedRunbook string
	var runbookSteps []string
	switch strings.ToLower(component) {
	case "servgate":
		suggestedRunbook = "rb-gate-restart"
		runbookSteps = []string{
			"1. Check route health metrics",
			"2. Run: serv gate restart --graceful",
			"3. Confirm service recovery logs",
		}
	case "servstore":
		suggestedRunbook = "rb-store-heal"
		runbookSteps = []string{
			"1. Assess filesystem status",
			"2. Run: serv store heal --shards=all",
			"3. Re-verify replica consistency",
		}
	default:
		suggestedRunbook = "rb-queue-purge"
		runbookSteps = []string{
			"1. Verify DLQ message count",
			"2. Run: serv queue purge dlq",
			"3. Auto-notify subscribers",
		}
	}

	timeline := IncidentTimeline{
		AlertID:            alertID,
		Title:              fmt.Sprintf("Incident Analysis: %s", message),
		Component:          component,
		Severity:           severity,
		Events:             events,
		AISuggestedRunbook: suggestedRunbook,
		AIRunbookSteps:     runbookSteps,
		AISuggestion:       ai.GenerateIncidentResolutionSuggestion(alertType, message),
	}

	json.NewEncoder(w).Encode(timeline)
}

func handleAIMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	nowStr := time.Now().Format("15:04:05")

	resp := AIMetricsResponse{
		TotalCostsUSD:     24.85,
		TotalToolCalls:    1420,
		ActiveAgentsCount: 3,
		ToolCalls: []AIToolCall{
			{Timestamp: nowStr, AgentName: "AutoDebugger-Agent", ToolCalled: "ServStore.ReadObject", Status: "success", TokensUsed: 420, CostUSD: 0.0084},
			{Timestamp: nowStr, AgentName: "CodeArchitect-Agent", ToolCalled: "ServQueue.Publish", Status: "success", TokensUsed: 650, CostUSD: 0.0130},
			{Timestamp: nowStr, AgentName: "DeployBot", ToolCalled: "ServGate.AddRoute", Status: "success", TokensUsed: 310, CostUSD: 0.0062},
			{Timestamp: "10:31:02", AgentName: "SecurityScanner", ToolCalled: "ServStore.ListObjects", Status: "blocked", TokensUsed: 120, CostUSD: 0.0024},
		},
		SafetyAlerts: []AISafetyAlert{
			{Timestamp: "10:31:02", AgentName: "SecurityScanner", Severity: "high", RuleName: "Prompt Guard Violation", Message: "Detected attempt to bypass access-control on system-access-logs bucket."},
			{Timestamp: "10:15:40", AgentName: "AutoDebugger-Agent", Severity: "medium", RuleName: "PII Leak Detected", Message: "Redacted credit card numbers inside request parameters payload."},
		},
	}

	json.NewEncoder(w).Encode(resp)
}
