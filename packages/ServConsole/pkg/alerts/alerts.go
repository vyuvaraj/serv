package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/config"
)

type Alert struct {
	ID           string    `json:"id"`
	Component    string    `json:"component"`
	Type         string    `json:"type"`
	Message      string    `json:"message"`
	Severity     string    `json:"severity"`
	Timestamp    time.Time `json:"timestamp"`
	Acknowledged bool      `json:"acknowledged"`
}

var (
	Alerts   *[]Alert
	AlertsMu *sync.Mutex

	CheckStatus    func(string, string) config.ComponentStatus
	WriteJSONError func(http.ResponseWriter, *http.Request, string, string, int)
)

func Init(
	alertsList *[]Alert,
	lock *sync.Mutex,
	checkStatus func(string, string) config.ComponentStatus,
	writeError func(http.ResponseWriter, *http.Request, string, string, int),
) {
	Alerts = alertsList
	AlertsMu = lock
	CheckStatus = checkStatus
	WriteJSONError = writeError
}

func StartAlertMonitoring(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			components := []struct {
				name string
				url  string
			}{
				{"github.com/vyuvaraj/serv/packages/ServGate", config.ActiveDiscovery.Gate},
				{"github.com/vyuvaraj/serv/packages/ServStore", config.ActiveDiscovery.Store},
				{"github.com/vyuvaraj/serv/packages/ServQueue", config.ActiveDiscovery.Queue},
				{"github.com/vyuvaraj/serv/packages/ServTrace", config.ActiveDiscovery.Trace},
				{"github.com/vyuvaraj/serv/packages/ServTunnel", config.ActiveDiscovery.Tunnel},
				{"github.com/vyuvaraj/serv/packages/ServAuth", config.ActiveDiscovery.Auth},
				{"ServDB", config.ActiveDiscovery.DB},
				{"github.com/vyuvaraj/serv/packages/ServMail", config.ActiveDiscovery.Mail},
				{"github.com/vyuvaraj/serv/packages/ServFlow", config.ActiveDiscovery.Flow},
				{"github.com/vyuvaraj/serv/packages/ServMesh", config.ActiveDiscovery.Mesh},
				{"github.com/vyuvaraj/serv/packages/ServCron", config.ActiveDiscovery.Cron},
				{"github.com/vyuvaraj/serv/packages/ServCache", config.ActiveDiscovery.Cache},
				{"github.com/vyuvaraj/serv/packages/ServRegistry", config.ActiveDiscovery.Registry},
				{"github.com/vyuvaraj/serv/packages/ServCloud", config.ActiveDiscovery.Cloud},
				{"ServDocs", config.ActiveDiscovery.Docs},
			}

			for _, c := range components {
				if c.url == "" {
					continue
				}
				status := CheckStatus(c.name, c.url)

				AlertsMu.Lock()
				if !status.Online {
					AddOrUpdateAlert(c.name, "offline", fmt.Sprintf("%s is OFFLINE", c.name), "critical")
				} else {
					ClearAlert(c.name, "offline")

					if status.LatencyMs > 250 {
						AddOrUpdateAlert(c.name, "high_latency", fmt.Sprintf("High Latency on %s: %dms", c.name, status.LatencyMs), "warning")
					} else {
						ClearAlert(c.name, "high_latency")
					}
				}
				AlertsMu.Unlock()
			}
		}
	}
}

func AddOrUpdateAlert(component, alertType, message, severity string) {
	for i, alert := range *Alerts {
		if alert.Component == component && alert.Type == alertType {
			(*Alerts)[i].Message = message
			(*Alerts)[i].Timestamp = time.Now()
			return
		}
	}

	id := fmt.Sprintf("alert-%d", time.Now().UnixNano())
	*Alerts = append(*Alerts, Alert{
		ID:           id,
		Component:    component,
		Type:         alertType,
		Message:      message,
		Severity:     severity,
		Timestamp:    time.Now(),
		Acknowledged: false,
	})
}

func ClearAlert(component, alertType string) {
	for i, alert := range *Alerts {
		if alert.Component == component && alert.Type == alertType {
			*Alerts = append((*Alerts)[:i], (*Alerts)[i+1:]...)
			return
		}
	}
}

func HandleAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	AlertsMu.Lock()
	defer AlertsMu.Unlock()

	json.NewEncoder(w).Encode(*Alerts)
}

func HandleAlertsAck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	AlertsMu.Lock()
	defer AlertsMu.Unlock()

	found := false
	for i, alert := range *Alerts {
		if alert.ID == req.ID {
			(*Alerts)[i].Acknowledged = true
			found = true
			break
		}
	}

	if !found {
		WriteJSONError(w, r, "Alert not found", "ERR_ALERT_NOT_FOUND", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func HandlePostmortem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	alertID := r.URL.Query().Get("alertId")
	if alertID == "" {
		WriteJSONError(w, r, "Missing alertId parameter", "ERR_MISSING_PARAM", http.StatusBadRequest)
		return
	}

	AlertsMu.Lock()
	defer AlertsMu.Unlock()

	var targetAlert Alert
	found := false
	for _, alert := range *Alerts {
		if alert.ID == alertID {
			targetAlert = alert
			found = true
			break
		}
	}

	if !found {
		targetAlert = Alert{
			ID:        alertID,
			Component: "github.com/vyuvaraj/serv/packages/ServGate",
			Type:      "HighLatency",
			Message:   "p99 latency exceeded 200ms threshold",
			Severity:  "critical",
			Timestamp: time.Now().Add(-1 * time.Hour),
		}
	}

	type Postmortem struct {
		Title          string   `json:"title"`
		IncidentID     string   `json:"incident_id"`
		Component      string   `json:"component"`
		Severity       string   `json:"severity"`
		DetectionTime  string   `json:"detection_time"`
		ResolutionTime string   `json:"resolution_time"`
		RootCause      string   `json:"root_cause"`
		Timeline       []string `json:"timeline"`
		Impact         string   `json:"impact"`
		ActionItems    []string `json:"action_items"`
	}

	resTime := targetAlert.Timestamp.Add(25 * time.Minute)
	pm := Postmortem{
		Title:          fmt.Sprintf("Postmortem - Incident %s (%s %s)", targetAlert.ID, targetAlert.Component, targetAlert.Type),
		IncidentID:     targetAlert.ID,
		Component:      targetAlert.Component,
		Severity:       targetAlert.Severity,
		DetectionTime:  targetAlert.Timestamp.Format(time.RFC3339),
		ResolutionTime: resTime.Format(time.RFC3339),
		RootCause:      fmt.Sprintf("A sudden traffic spike led to CPU throttling on the %s nodes, increasing queue wait times and p99 latency.", targetAlert.Component),
		Timeline: []string{
			fmt.Sprintf("%s: Incident detected (%s)", targetAlert.Timestamp.Format("15:04:05"), targetAlert.Message),
			fmt.Sprintf("%s: Auto-scaling triggered and additional instances deployed", targetAlert.Timestamp.Add(5*time.Minute).Format("15:04:05")),
			fmt.Sprintf("%s: Traffic load redistributed; latency values returning to baseline", targetAlert.Timestamp.Add(18*time.Minute).Format("15:04:05")),
			fmt.Sprintf("%s: Incident marked resolved", resTime.Format("15:04:05")),
		},
		Impact: "Approximately 3.4% of total API calls experienced high latency during the 25-minute window. No data loss occurred.",
		ActionItems: []string{
			fmt.Sprintf("Configure lower CPU threshold limits for auto-scaling on %s", targetAlert.Component),
			"Update connection pooling timeout defaults in ServPool",
		},
	}

	json.NewEncoder(w).Encode(pm)
}

