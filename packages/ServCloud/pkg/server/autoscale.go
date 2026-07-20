//go:build enterprise

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/serv/packages/ServCloud/pkg/orchestrator"
)

const IsAutoscaleSupported = true

type gatewayConsoleSyncResponse struct {
	Routes []struct {
		Prefix  string   `json:"prefix"`
		Target  string   `json:"target"`
		Targets []string `json:"targets"`
	} `json:"routes"`
	ActiveConnections map[string]int `json:"active_connections"`
}

var (
	replicasMu sync.Mutex
	activeReplicas = make(map[string][]string)
)

func (s *Server) StartAutoscaleLoop() {
	if s.gatewayURL == "" {
		return
	}

	s.autoscaleTicker = time.NewTicker(2 * time.Second)
	go func() {
		for range s.autoscaleTicker.C {
			s.checkAutoscale()
		}
	}()
}

func (s *Server) StopAutoscaleLoopForTest() {
	if s.autoscaleTicker != nil {
		s.autoscaleTicker.Stop()
	}
}

func (s *Server) checkAutoscale() {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest("GET", s.gatewayURL+"/api/console/sync", nil)
	if err != nil {
		return
	}
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var data gatewayConsoleSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}

	services := s.orch.ListServices()
	for _, svc := range services {
		name := svc.Name
		if strings.Contains(name, "-replica-") {
			continue
		}

		targetURL := fmt.Sprintf("http://localhost:%d", svc.Port)
		conns := data.ActiveConnections[targetURL]

		replicasMu.Lock()
		reps := activeReplicas[name]
		replicasMu.Unlock()

		if conns > 3 && len(reps) < 3 {
			log.Printf("[AUTOSCALER] High load detected on service %s (%d active connections). Scaling UP...", name, conns)
			s.scaleUp(name, svc)
		} else if conns <= 1 && len(reps) > 0 {
			log.Printf("[AUTOSCALER] Low load detected on service %s (%d active connections). Scaling DOWN...", name, conns)
			s.scaleDown(name, svc)
		} else if conns == 0 && len(reps) == 0 && (svc.Status == "running" || svc.Status == "unhealthy") {
			// Scale to Zero
			tIdle, exists := idleStart[name]
			if !exists {
				idleStart[name] = time.Now()
			} else if time.Since(tIdle) > 5*time.Second {
				log.Printf("[AUTOSCALER] Service %s has been idle. Scaling to ZERO...", name)
				s.scaleToZero(name, svc)
			}
		} else {
			delete(idleStart, name)
		}
	}
}

var (
	idleStart = make(map[string]time.Time)
)

func (s *Server) scaleToZero(name string, mainSvc *orchestrator.ServiceProcess) {
	_ = s.orch.StopService(name)
	delete(idleStart, name)

	// Route to activator endpoint on ServCloud
	activatorURL := fmt.Sprintf("http://localhost:8085/api/services/%s/invoke", name)
	payload := map[string]interface{}{
		"prefix":  fmt.Sprintf("/service/%s", name),
		"target":  activatorURL,
		"targets": []string{activatorURL},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", s.gatewayURL+"/api/routes", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Server) scaleUp(name string, mainSvc *orchestrator.ServiceProcess) {
	history := s.orch.GetHistory()
	var latestCode string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].ServiceName == name {
			latestCode = history[i].Code
			break
		}
	}

	if latestCode == "" {
		return
	}

	replicasMu.Lock()
	reps := activeReplicas[name]
	replicaName := fmt.Sprintf("%s-replica-%d", name, len(reps)+1)
	replicasMu.Unlock()

	replicaSvc, err := s.orch.DeployWithEnv(replicaName, latestCode, mainSvc.Env)
	if err != nil {
		log.Printf("[AUTOSCALER] Failed to scale up replica %s: %v", replicaName, err)
		return
	}

	replicasMu.Lock()
	activeReplicas[name] = append(activeReplicas[name], replicaName)
	newReps := activeReplicas[name]
	replicasMu.Unlock()

	_ = replicaSvc

	s.syncGateRoute(name, mainSvc, newReps)
}

func (s *Server) scaleDown(name string, mainSvc *orchestrator.ServiceProcess) {
	replicasMu.Lock()
	reps := activeReplicas[name]
	if len(reps) == 0 {
		replicasMu.Unlock()
		return
	}
	latestReplica := reps[len(reps)-1]
	activeReplicas[name] = reps[:len(reps)-1]
	newReps := activeReplicas[name]
	replicasMu.Unlock()

	_ = s.orch.Undeploy(latestReplica)

	s.syncGateRoute(name, mainSvc, newReps)
}

func (s *Server) syncGateRoute(name string, mainSvc *orchestrator.ServiceProcess, reps []string) {
	targets := []string{fmt.Sprintf("http://localhost:%d", mainSvc.Port)}
	for _, repName := range reps {
		if rep, ok := s.orch.GetService(repName); ok {
			targets = append(targets, fmt.Sprintf("http://localhost:%d", rep.Port))
		}
	}

	payload := map[string]interface{}{
		"prefix":  fmt.Sprintf("/service/%s", name),
		"target":  fmt.Sprintf("http://localhost:%d", mainSvc.Port),
		"targets": targets,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", s.gatewayURL+"/api/routes", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
