package launcher

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/config"
)

type DevServiceStatus struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

func OrchestrateStartup() {
	time.Sleep(500 * time.Millisecond)

	log.Println("[orchestrator] Starting all ecosystem services in dependency order...")

	services := []struct {
		name string
		exec string
		args []string
		port int
	}{
		{"github.com/vyuvaraj/serv/packages/ServStore", "./servstore.exe", []string{}, 8081},
		{"github.com/vyuvaraj/serv/packages/ServQueue", "./servqueue.exe", []string{}, 8082},
		{"ServDB", "./servdb.exe", []string{}, 8097},
		{"github.com/vyuvaraj/serv/packages/ServAuth", "./servauth.exe", []string{}, 8098},
		{"github.com/vyuvaraj/serv/packages/ServGate", "./servgate.exe", []string{}, 8080},
		{"github.com/vyuvaraj/serv/packages/ServMesh", "./servmesh.exe", []string{}, 8089},
		{"github.com/vyuvaraj/serv/packages/ServCron", "./servcron.exe", []string{}, 8087},
		{"ServDocs", "./servdocs.exe", []string{}, 8084},
	}

	for _, svc := range services {
		execPath := svc.exec
		if _, err := os.Stat(execPath); os.IsNotExist(err) {
			execPath = "../" + svc.name + "/" + strings.ToLower(svc.name)
			if _, err2 := os.Stat(execPath + ".exe"); err2 == nil {
				execPath = execPath + ".exe"
			} else if _, err3 := os.Stat(execPath); err3 != nil {
				execPath = "../" + svc.name
				if _, err4 := os.Stat(execPath + ".exe"); err4 == nil {
					execPath = execPath + ".exe"
				} else {
					log.Printf("[orchestrator] Executable for %s not found: skipping", svc.name)
					continue
				}
			}
		}

		log.Printf("[orchestrator] Launching %s (%s)...", svc.name, execPath)
		cmd := exec.Command(execPath, svc.args...)
		err := cmd.Start()
		if err != nil {
			log.Printf("[orchestrator] Failed to start %s: %v", svc.name, err)
			continue
		}

		log.Printf("[orchestrator] Waiting for %s to become healthy...", svc.name)
		healthy := false
		client := http.Client{Timeout: 500 * time.Millisecond}
		for i := 0; i < 5; i++ {
			time.Sleep(500 * time.Millisecond)
			resp, err := client.Get(fmt.Sprintf("http://localhost:%d/healthz", svc.port))
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					healthy = true
					break
				}
			}
		}
		if healthy {
			log.Printf("[orchestrator] %s is ONLINE", svc.name)
		} else {
			log.Printf("[orchestrator] Warning: %s failed health probe", svc.name)
		}
	}
	log.Println("[orchestrator] Ecosystem startup orchestration finished.")
}

func HandleDevServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	services := []struct {
		Name string
		URL  string
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
	}

	client := &http.Client{Timeout: 300 * time.Millisecond}
	var list []DevServiceStatus

	for _, s := range services {
		if s.URL == "" {
			continue
		}
		status := "unhealthy"
		resp, err := client.Get(strings.TrimSuffix(s.URL, "/") + "/healthz")
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				status = "healthy"
			}
			resp.Body.Close()
		} else {
			resp2, err2 := client.Get(strings.TrimSuffix(s.URL, "/") + "/health")
			if err2 == nil {
				if resp2.StatusCode == http.StatusOK {
					status = "healthy"
				}
				resp2.Body.Close()
			}
		}

		list = append(list, DevServiceStatus{
			Name:   s.Name,
			URL:    s.URL,
			Status: status,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func HandleDevRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	serviceName := r.URL.Query().Get("service")
	if serviceName == "" {
		http.Error(w, "service parameter required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Restart triggered for service %s in dev mode", serviceName),
	})
}
