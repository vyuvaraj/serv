package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runMeshInspect prints the live state of the ServMesh service registry.
// Usage: serv mesh inspect [--host <url>] [--service <name>]
func runMeshInspect() {
	host := "http://localhost:8083"
	service := ""

	if envHost := os.Getenv("SERV_MESH_URL"); envHost != "" {
		host = envHost
	}

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--host", "-host":
			if i+1 < len(os.Args) {
				host = os.Args[i+1]
				i++
			}
		case "--service", "-service", "-s":
			if i+1 < len(os.Args) {
				service = os.Args[i+1]
				i++
			}
		}
	}

	url := fmt.Sprintf("%s/api/instances", strings.TrimSuffix(host, "/"))
	if service != "" {
		url = fmt.Sprintf("%s/api/instances?service=%s", strings.TrimSuffix(host, "/"), service)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}
	if token := os.Getenv("SERV_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error connecting to ServMesh at %s: %v\n", host, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("ServMesh returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var instances []map[string]interface{}
	if err := json.Unmarshal(body, &instances); err != nil {
		fmt.Println(string(body))
		return
	}

	if len(instances) == 0 {
		if service != "" {
			fmt.Printf("No instances found for service %q\n", service)
		} else {
			fmt.Println("No services registered in ServMesh.")
		}
		return
	}

	fmt.Printf("ServMesh Registry  (%d instances)\n", len(instances))
	fmt.Println(strings.Repeat("─", 70))
	fmt.Printf("%-20s  %-30s  %-8s  %s\n", "SERVICE", "ADDRESS", "VERSION", "LAST SEEN")
	fmt.Println(strings.Repeat("─", 70))
	for _, inst := range instances {
		svc, _ := inst["service"].(string)
		addr, _ := inst["address"].(string)
		ver, _ := inst["version"].(string)
		if ver == "" {
			ver = "—"
		}
		lastSeen, _ := inst["last_seen"].(string)
		if len(lastSeen) > 19 {
			lastSeen = lastSeen[:19]
		}
		fmt.Printf("%-20s  %-30s  %-8s  %s\n", svc, addr, ver, lastSeen)
	}
}

// runMeshRoutes prints the active routing rules and circuit-breaker state.
// Usage: serv mesh routes [--host <url>]
func runMeshRoutes() {
	host := "http://localhost:8083"
	if envHost := os.Getenv("SERV_MESH_URL"); envHost != "" {
		host = envHost
	}
	for i := 3; i < len(os.Args); i++ {
		if (os.Args[i] == "--host" || os.Args[i] == "-host") && i+1 < len(os.Args) {
			host = os.Args[i+1]
			i++
		}
	}

	url := fmt.Sprintf("%s/api/routing/rules", strings.TrimSuffix(host, "/"))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Failed to create request: %v\n", err)
		os.Exit(1)
	}
	if token := os.Getenv("SERV_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error connecting to ServMesh at %s: %v\n", host, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("ServMesh returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var rules []map[string]interface{}
	if err := json.Unmarshal(body, &rules); err != nil {
		fmt.Println(string(body))
		return
	}

	if len(rules) == 0 {
		fmt.Println("No routing rules configured.")
		return
	}

	fmt.Printf("%-20s  %-10s  %-10s  %s\n", "SERVICE", "WEIGHT", "CB STATE", "DESTINATION")
	fmt.Println(strings.Repeat("─", 70))
	for _, r := range rules {
		svc, _ := r["service"].(string)
		weight := fmt.Sprintf("%v", r["weight"])
		cb, _ := r["circuit_breaker"].(string)
		if cb == "" {
			cb = "closed"
		}
		dest, _ := r["destination"].(string)
		fmt.Printf("%-20s  %-10s  %-10s  %s\n", svc, weight, cb, dest)
	}
}
