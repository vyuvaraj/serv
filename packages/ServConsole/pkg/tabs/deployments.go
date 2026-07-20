package tabs

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/alerts"
)

type Deployment struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Author    string    `json:"author"`
	Status    string    `json:"status"` // active, rolled_back, historical
	Changelog string    `json:"changelog"`
}

type EnvironmentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ThemeDot    string `json:"themeDot"`
	Description string `json:"description"`
}

type RunbookAction struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Component   string `json:"component"`
	Command     string `json:"command"`
}

type Playbook struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Trigger     string `json:"trigger"`
	Steps       int    `json:"steps"`
	LastRun     string `json:"lastRun"`
	AutoExecute bool   `json:"autoExecute"`
}

type PlaybookExecResult struct {
	PlaybookID string `json:"playbookId"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	StepsRun   int    `json:"stepsRun"`
}

var (
	Environments = []EnvironmentInfo{
		{ID: "development", Name: "Development", ThemeDot: "#06b6d4", Description: "Local testing and debugging playground"},
		{ID: "staging", Name: "Staging", ThemeDot: "#f59e0b", Description: "Integration testing and candidate releases"},
		{ID: "production", Name: "Production", ThemeDot: "#a855f7", Description: "Live user-facing ecosystem runtime"},
	}
	ActiveEnvironment = "development"
	EnvMu             sync.Mutex

	Runbooks = []RunbookAction{
		{ID: "rb-gate-restart", Name: "Restart ServGate Instance", Description: "Drain connections and perform graceful restart of the Gateway process", Component: "github.com/vyuvaraj/serv/packages/ServGate", Command: "serv gate restart --graceful"},
		{ID: "rb-gate-cache", Name: "Clear ServGate Router Cache", Description: "Purge all compiled semantic cache entries in Gateway memory", Component: "github.com/vyuvaraj/serv/packages/ServGate", Command: "serv gate cache purge"},
		{ID: "rb-store-heal", Name: "Rebalance ServStore Storage Shards", Description: "Initiate P2P healing across active data shards and rebuild parity partitions", Component: "github.com/vyuvaraj/serv/packages/ServStore", Command: "serv store heal --shards=all"},
		{ID: "rb-queue-purge", Name: "Flush Dead Letter Queue (DLQ)", Description: "Clear stale or rejected messages in ServQueue DLQ namespaces", Component: "github.com/vyuvaraj/serv/packages/ServQueue", Command: "serv queue purge dlq"},
	}
	RunbooksMu sync.Mutex

	AlertResolutions   = make(map[string]int)
	AlertResolutionsMu sync.Mutex
)

func HandleDeployments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	DeploymentsMu.Lock()
	defer DeploymentsMu.Unlock()
	json.NewEncoder(w).Encode(*Deployments)
}

func HandleRollback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TargetID string `json:"targetId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	DeploymentsMu.Lock()
	defer DeploymentsMu.Unlock()

	foundIndex := -1
	for i, d := range *Deployments {
		if d.ID == req.TargetID {
			foundIndex = i
			break
		}
	}

	if foundIndex == -1 {
		WriteJSONError(w, r, "Target deployment not found", "ERR_DEPLOYMENT_NOT_FOUND", http.StatusNotFound)
		return
	}

	for i, d := range *Deployments {
		if d.Status == "active" {
			(*Deployments)[i].Status = "historical"
		}
	}

	targetDep := (*Deployments)[foundIndex]
	newID := fmt.Sprintf("dep-%d", time.Now().UnixNano())
	newVersion := targetDep.Version + "-rollback"
	newDep := Deployment{
		ID:        newID,
		Version:   newVersion,
		Timestamp: time.Now(),
		Author:    "console-operator",
		Status:    "active",
		Changelog: fmt.Sprintf("Rollback to version %s (from %s)", targetDep.Version, targetDep.ID),
	}

	*Deployments = append([]Deployment{newDep}, *Deployments...)

	json.NewEncoder(w).Encode(map[string]any{
		"success":    true,
		"deployment": newDep,
	})
}

func HandleEnvironments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	EnvMu.Lock()
	defer EnvMu.Unlock()
	json.NewEncoder(w).Encode(map[string]any{
		"active":       ActiveEnvironment,
		"environments": Environments,
	})
}

func HandleSelectEnvironment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		EnvironmentID string `json:"environmentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	EnvMu.Lock()
	defer EnvMu.Unlock()

	valid := false
	for _, env := range Environments {
		if env.ID == req.EnvironmentID {
			valid = true
			break
		}
	}

	if !valid {
		WriteJSONError(w, r, "Invalid environment ID", "ERR_INVALID_ENVIRONMENT", http.StatusBadRequest)
		return
	}

	ActiveEnvironment = req.EnvironmentID
	log.Printf("[environment] Switched active environment to: %s", ActiveEnvironment)

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"active":  ActiveEnvironment,
	})
}

func HandleRunbooks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	compFilter := r.URL.Query().Get("component")

	RunbooksMu.Lock()
	defer RunbooksMu.Unlock()

	filtered := []RunbookAction{}
	for _, rb := range Runbooks {
		if compFilter == "" || strings.EqualFold(rb.Component, compFilter) {
			filtered = append(filtered, rb)
		}
	}

	if compFilter != "" {
		AlertResolutionsMu.Lock()
		resolutionsCount := AlertResolutions[compFilter]
		AlertResolutionsMu.Unlock()

		if resolutionsCount >= 3 || r.URL.Query().Get("force_generation") == "true" {
			filtered = append(filtered, RunbookAction{
				ID:          "rb-auto-generated-" + strings.ToLower(compFilter),
				Name:        "Automated Runbook for " + compFilter,
				Description: "Auto-generated runbook compiled from 3 past manual resolutions of " + compFilter + " alerts.",
				Component:   compFilter,
				Command:     fmt.Sprintf("serv %s restart --graceful", strings.ToLower(compFilter)),
			})
		}
	}

	json.NewEncoder(w).Encode(filtered)
}

func HandleExecuteRunbook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		RunbookID string `json:"runbookId"`
		AlertID   string `json:"alertId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	RunbooksMu.Lock()
	var targetRb *RunbookAction
	for _, rb := range Runbooks {
		if rb.ID == req.RunbookID {
			targetRb = &rb
			break
		}
	}
	RunbooksMu.Unlock()

	if targetRb == nil {
		WriteJSONError(w, r, "Runbook not found", "ERR_RUNBOOK_NOT_FOUND", http.StatusNotFound)
		return
	}

	if AddAuditLog != nil {
		AddAuditLog("console-operator", fmt.Sprintf("Runbook %s: %s", targetRb.Name, targetRb.Command), r.Method, r.URL.Path, http.StatusOK)
	}

	if req.AlertID != "" {
		alerts.AlertsMu.Lock()
		resolvedComponent := ""
		for i, a := range *alerts.Alerts {
			if a.ID == req.AlertID {
				(*alerts.Alerts)[i].Acknowledged = true
				resolvedComponent = a.Component
				break
			}
		}
		alerts.AlertsMu.Unlock()

		if resolvedComponent != "" {
			AlertResolutionsMu.Lock()
			AlertResolutions[resolvedComponent]++
			AlertResolutionsMu.Unlock()
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": fmt.Sprintf("Runbook %s executed successfully.", targetRb.Name),
		"log":     fmt.Sprintf("Command '%s' exited with code 0.", targetRb.Command),
	})
}

func HandlePlaybooks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	playbooks := []Playbook{
		{
			ID:          "pb-db-pool-exhaustion",
			Name:        "DB Connection Pool Exhaustion",
			Trigger:     "ServDB active_connections > 90% of max_connections for 60s",
			Steps:       4,
			LastRun:     "3 days ago",
			AutoExecute: true,
		},
		{
			ID:          "pb-high-latency-gate",
			Name:        "ServGate High Latency Mitigation",
			Trigger:     "ServGate p99 latency > 500ms for 30s",
			Steps:       3,
			LastRun:     "Never",
			AutoExecute: false,
		},
		{
			ID:          "pb-disk-pressure",
			Name:        "ServStore Disk Pressure Relief",
			Trigger:     "ServStore disk_usage_pct > 85%",
			Steps:       2,
			LastRun:     "12 days ago",
			AutoExecute: true,
		},
	}

	json.NewEncoder(w).Encode(playbooks)
}

func HandleExecutePlaybook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		WriteJSONError(w, r, "Missing playbook 'id' parameter", "ERR_MISSING_PARAM", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(PlaybookExecResult{
		PlaybookID: id,
		Status:     "success",
		Message:    "Playbook executed successfully. All steps completed.",
		StepsRun:   3,
	})
}
