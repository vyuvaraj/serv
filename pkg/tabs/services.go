package tabs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/vyuvaraj/ServShared"

	"servconsole/pkg/config"
)

func HandleConsoleCronJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	targetURL := fmt.Sprintf("%s/api/jobs", strings.TrimSuffix(config.ActiveDiscovery.Cron, "/"))

	if r.Method == http.MethodGet {
		req, _ := http.NewRequest("GET", targetURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			WriteJSONError(w, r, "Failed to fetch jobs from ServCron: "+err.Error(), "ERR_CRON_UNREACHABLE", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	if r.Method == http.MethodPost {
		req, _ := http.NewRequest("POST", targetURL, r.Body)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			WriteJSONError(w, r, "Failed to create job in ServCron: "+err.Error(), "ERR_CRON_UNREACHABLE", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if AddAuditLog != nil {
			AddAuditLog(r.Header.Get("X-Console-User"), "Create Cron Job", r.Method, r.URL.Path, resp.StatusCode)
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
}

func HandleConsoleCronJobsItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimPrefix(r.URL.Path, "/api/cron/jobs/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		WriteJSONError(w, r, "Missing job ID", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}

	jobID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	client := &http.Client{Timeout: 3 * time.Second}
	var targetURL string
	if action == "run" {
		targetURL = fmt.Sprintf("%s/api/jobs/%s/run", strings.TrimSuffix(config.ActiveDiscovery.Cron, "/"), jobID)
	} else {
		targetURL = fmt.Sprintf("%s/api/jobs/%s", strings.TrimSuffix(config.ActiveDiscovery.Cron, "/"), jobID)
	}

	if r.Method == http.MethodPost && action == "run" {
		req, _ := http.NewRequest("POST", targetURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			WriteJSONError(w, r, "Failed to trigger job in ServCron: "+err.Error(), "ERR_CRON_UNREACHABLE", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if AddAuditLog != nil {
			AddAuditLog(r.Header.Get("X-Console-User"), "Manually Trigger Cron Job: "+jobID, r.Method, r.URL.Path, resp.StatusCode)
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	if r.Method == http.MethodDelete && action == "" {
		req, _ := http.NewRequest("DELETE", targetURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			WriteJSONError(w, r, "Failed to delete job in ServCron: "+err.Error(), "ERR_CRON_UNREACHABLE", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if AddAuditLog != nil {
			AddAuditLog(r.Header.Get("X-Console-User"), "Delete Cron Job: "+jobID, r.Method, r.URL.Path, resp.StatusCode)
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	WriteJSONError(w, r, "Method not allowed or invalid action", "ERR_BAD_REQUEST", http.StatusBadRequest)
}

func HandleConsoleCacheStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	targetURL := fmt.Sprintf("%s/api/cache/inspect", strings.TrimSuffix(config.ActiveDiscovery.Cache, "/"))

	req, _ := http.NewRequest("GET", targetURL, nil)

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to fetch stats from ServCache: "+err.Error(), "ERR_CACHE_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleConsoleCacheClear(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	pattern := r.URL.Query().Get("pattern")
	targetURL := fmt.Sprintf("%s/api/cache", strings.TrimSuffix(config.ActiveDiscovery.Cache, "/"))
	if pattern != "" {
		targetURL = fmt.Sprintf("%s/api/cache?pattern=%s", strings.TrimSuffix(config.ActiveDiscovery.Cache, "/"), url.QueryEscape(pattern))
	}

	req, _ := http.NewRequest("DELETE", targetURL, nil)

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to clear ServCache: "+err.Error(), "ERR_CACHE_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if AddAuditLog != nil {
		AddAuditLog(r.Header.Get("X-Console-User"), "Clear Cache: "+pattern, r.Method, r.URL.Path, resp.StatusCode)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleConsoleMeshInstances(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	targetURL := fmt.Sprintf("%s/api/instances", strings.TrimSuffix(config.ActiveDiscovery.Mesh, "/"))

	req, _ := http.NewRequest("GET", targetURL, nil)

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to fetch instances from ServMesh: "+err.Error(), "ERR_MESH_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleConsoleRegistryPackages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	targetURL := fmt.Sprintf("%s/api/packages", strings.TrimSuffix(config.ActiveDiscovery.Registry, "/"))

	req, _ := http.NewRequest("GET", targetURL, nil)

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to fetch packages from ServRegistry: "+err.Error(), "ERR_REGISTRY_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleConsoleCloudServices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	targetURL := fmt.Sprintf("%s/api/services", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"))

	req, _ := http.NewRequest("GET", targetURL, nil)

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to fetch services from ServCloud: "+err.Error(), "ERR_CLOUD_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleConsoleCloudServicesItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimPrefix(r.URL.Path, "/api/cloud/services/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		WriteJSONError(w, r, "Missing service name", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}

	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	client := &http.Client{Timeout: 3 * time.Second}
	var targetURL string
	switch action {
	case "logs":
		targetURL = fmt.Sprintf("%s/api/services/%s/logs", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"), name)
	case "stats":
		targetURL = fmt.Sprintf("%s/api/services/%s/stats", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"), name)
	case "rollback":
		targetURL = fmt.Sprintf("%s/api/services/%s/rollback", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"), name)
	case "env":
		targetURL = fmt.Sprintf("%s/api/services/%s/env", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"), name)
	default:
		targetURL = fmt.Sprintf("%s/api/services/%s", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"), name)
	}

	var req *http.Request
	if r.Method == http.MethodDelete && action == "" {
		req, _ = http.NewRequest("DELETE", targetURL, nil)
	} else if r.Method == http.MethodPost && (action == "rollback" || action == "env") {
		req, _ = http.NewRequest("POST", targetURL, r.Body)
	} else if r.Method == http.MethodGet {
		req, _ = http.NewRequest("GET", targetURL, nil)
	} else {
		WriteJSONError(w, r, "Method not allowed or invalid action", "ERR_BAD_REQUEST", http.StatusBadRequest)
		return
	}

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to communicate with ServCloud: "+err.Error(), "ERR_CLOUD_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if r.Method == http.MethodDelete {
		if AddAuditLog != nil {
			AddAuditLog(r.Header.Get("X-Console-User"), "Undeploy Service: "+name, r.Method, r.URL.Path, resp.StatusCode)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleConsoleCloudDeploy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	targetURL := fmt.Sprintf("%s/api/deploy", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"))

	req, _ := http.NewRequest("POST", targetURL, r.Body)
	req.Header.Set("Content-Type", "application/json")

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to deploy on ServCloud: "+err.Error(), "ERR_CLOUD_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if AddAuditLog != nil {
		AddAuditLog(r.Header.Get("X-Console-User"), "Deploy Service via Console", r.Method, r.URL.Path, resp.StatusCode)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleConsoleCloudHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	targetURL := fmt.Sprintf("%s/api/history", strings.TrimSuffix(config.ActiveDiscovery.Cloud, "/"))

	req, _ := http.NewRequest("GET", targetURL, nil)

	if jwtSec := os.Getenv("SERV_JWT_SECRET"); jwtSec != "" {
		svcToken, _ := ServShared.GenerateServiceToken(jwtSec, "servconsole")
		if svcToken != "" {
			req.Header.Set("Authorization", "Bearer "+svcToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to fetch history from ServCloud: "+err.Error(), "ERR_CLOUD_UNREACHABLE", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func HandleDocsSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	specs := make(map[string]any)
	services := []struct {
		name string
		url  string
	}{
		{"ServGate", config.ActiveDiscovery.Gate},
		{"ServStore", config.ActiveDiscovery.Store},
		{"ServQueue", config.ActiveDiscovery.Queue},
		{"ServAuth", config.ActiveDiscovery.Auth},
		{"ServDB", config.ActiveDiscovery.DB},
	}

	client := http.Client{Timeout: 500 * time.Millisecond}
	for _, svc := range services {
		if svc.url == "" {
			continue
		}
		specUrl := fmt.Sprintf("%s/openapi.json", strings.TrimSuffix(svc.url, "/"))
		resp, err := client.Get(specUrl)
		if err == nil && resp.StatusCode == http.StatusOK {
			var specData any
			if err2 := json.NewDecoder(resp.Body).Decode(&specData); err2 == nil {
				specs[svc.name] = specData
				resp.Body.Close()
				continue
			}
			resp.Body.Close()
		}

		specs[svc.name] = map[string]any{
			"openapi": "3.0.0",
			"info": map[string]any{
				"title":       svc.name + " API Portal",
				"version":     "1.0.0",
				"description": fmt.Sprintf("Auto-discovered API endpoints for %s at %s", svc.name, svc.url),
			},
			"paths": map[string]any{
				"/healthz": map[string]any{
					"get": map[string]any{
						"summary": "Health status check",
						"responses": map[string]any{
							"200": map[string]any{
								"description": "Service is healthy",
							},
						},
					},
				},
			},
		}
	}

	json.NewEncoder(w).Encode(specs)
}

func HandleConsoleLocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	targetURL := fmt.Sprintf("%s/api/locks/observability", strings.TrimSuffix(config.ActiveDiscovery.Lock, "/"))
	client := http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		WriteJSONError(w, r, err.Error(), "ERR_INTERNAL", http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Tenant-ID", r.Header.Get("X-Tenant-ID"))
	req.Header.Set("Authorization", "Bearer "+config.ActiveDiscovery.AuthToken)

	resp, err := client.Do(req)
	if err != nil {
		WriteJSONError(w, r, "Failed to connect to lock service: "+err.Error(), "ERR_LOCK_CONNECT", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
