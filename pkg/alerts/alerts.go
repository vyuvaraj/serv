package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"servconsole/pkg/config"
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
				{"ServGate", config.ActiveDiscovery.Gate},
				{"ServStore", config.ActiveDiscovery.Store},
				{"ServQueue", config.ActiveDiscovery.Queue},
				{"ServTrace", config.ActiveDiscovery.Trace},
				{"ServTunnel", config.ActiveDiscovery.Tunnel},
				{"ServAuth", config.ActiveDiscovery.Auth},
				{"ServDB", config.ActiveDiscovery.DB},
				{"ServMail", config.ActiveDiscovery.Mail},
				{"ServFlow", config.ActiveDiscovery.Flow},
				{"ServMesh", config.ActiveDiscovery.Mesh},
				{"ServCron", config.ActiveDiscovery.Cron},
				{"ServCache", config.ActiveDiscovery.Cache},
				{"ServRegistry", config.ActiveDiscovery.Registry},
				{"ServCloud", config.ActiveDiscovery.Cloud},
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
