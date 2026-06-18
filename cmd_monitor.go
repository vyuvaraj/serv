package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type routeKey struct {
	method string
	route  string
}

type routeMetrics struct {
	requests   int64
	latencySum float64
}

func runMonitor(target string) {
	baseURL := target
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		// If it's just a port, e.g. "8080"
		if _, err := strconv.Atoi(baseURL); err == nil {
			baseURL = "http://localhost:" + baseURL
		} else {
			baseURL = "http://" + baseURL
		}
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	fmt.Printf("Connecting to metrics endpoint at %s/metrics...\n", baseURL)

	client := &http.Client{Timeout: 2 * time.Second}

	// For rate/latency calculations
	prevTime := time.Now()
	prevMetrics := make(map[routeKey]routeMetrics)

	// Refresh loop
	for {
		resp, err := client.Get(baseURL + "/metrics")
		if err != nil {
			fmt.Printf("\033[H\033[2J") // Clear screen
			fmt.Printf("\x1b[31;1mError connecting to service:\x1b[0m %v\n", err)
			fmt.Println("Retrying in 2 seconds...")
			time.Sleep(2 * time.Second)
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		// Query Health for uptime
		uptimeStr := "Unknown"
		healthResp, err := client.Get(baseURL + "/health")
		if err == nil {
			var healthData map[string]interface{}
			if json.NewDecoder(healthResp.Body).Decode(&healthData) == nil {
				if upt, exists := healthData["uptime"]; exists {
					uptimeStr = fmt.Sprint(upt)
				}
			}
			healthResp.Body.Close()
		}

		currTime := time.Now()
		elapsed := currTime.Sub(prevTime).Seconds()
		if elapsed <= 0 {
			elapsed = 1.0
		}

		// Parse metrics
		lines := strings.Split(string(bodyBytes), "\n")
		var goroutines int
		var memSys, memAlloc, memHeap int64
		currMetrics := make(map[routeKey]routeMetrics)

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			parts := strings.SplitN(line, " ", 2)
			if len(parts) != 2 {
				continue
			}
			metricKey := parts[0]
			valStr := parts[1]

			val, _ := strconv.ParseFloat(valStr, 64)

			if metricKey == "go_goroutines" {
				goroutines = int(val)
			} else if metricKey == "go_mem_sys_bytes" {
				memSys = int64(val)
			} else if metricKey == "go_mem_alloc_bytes" {
				memAlloc = int64(val)
			} else if metricKey == "go_mem_heap_alloc_bytes" {
				memHeap = int64(val)
			} else if strings.HasPrefix(metricKey, "http_server_requests_total") {
				labels := parseLabels(metricKey)
				rKey := routeKey{method: labels["method"], route: labels["route"]}
				stats := currMetrics[rKey]
				stats.requests = int64(val)
				currMetrics[rKey] = stats
			} else if strings.HasPrefix(metricKey, "http_server_request_duration_seconds") {
				labels := parseLabels(metricKey)
				rKey := routeKey{method: labels["method"], route: labels["route"]}
				stats := currMetrics[rKey]
				// Gauge or Histogram sum representation
				stats.latencySum = val
				currMetrics[rKey] = stats
			}
		}

		// Clear screen and draw
		fmt.Print("\033[H\033[2J")
		fmt.Printf("\x1b[36;1m========================================================================\x1b[0m\n")
		fmt.Printf("\x1b[32;1m                    SERV LIVE MONITORING DASHBOARD                      \x1b[0m\n")
		fmt.Printf("\x1b[36;1m========================================================================\x1b[0m\n")
		fmt.Printf(" \x1b[33;1mTarget:\x1b[0m      %-35s \x1b[33;1mUptime:\x1b[0m      %s\n", baseURL, uptimeStr)
		fmt.Printf(" \x1b[33;1mGoroutines:\x1b[0m  %-35d \x1b[33;1mAllocated:\x1b[0m   %.2f MB\n", goroutines, float64(memAlloc)/(1024*1024))
		fmt.Printf(" \x1b[33;1mHeap Alloc:\x1b[0m  %-35.2f MB \x1b[33;1mSys Memory:\x1b[0m  %.2f MB\n", float64(memHeap)/(1024*1024), float64(memSys)/(1024*1024))
		fmt.Printf("\x1b[36m------------------------------------------------------------------------\x1b[0m\n")
		fmt.Printf(" \x1b[37;1m%-6s  %-25s  %-12s  %-10s  %-12s\x1b[0m\n", "METHOD", "ROUTE", "REQUESTS", "RPS", "AVG LATENCY")
		fmt.Printf("\x1b[36m------------------------------------------------------------------------\x1b[0m\n")

		if len(currMetrics) == 0 {
			fmt.Println("  No active routes logged requests yet.")
		} else {
			for rKey, currVal := range currMetrics {
				prevVal, exists := prevMetrics[rKey]
				var rps float64
				if exists {
					rps = float64(currVal.requests-prevVal.requests) / elapsed
					if rps < 0 {
						rps = 0
					}
				}

				avgLatencyMs := 0.0
				if currVal.requests > 0 {
					// We stored latency duration in seconds. Convert to ms.
					avgLatencyMs = (currVal.latencySum / float64(currVal.requests)) * 1000.0
				}

				// Cap route label representation
				displayRoute := rKey.route
				if len(displayRoute) > 25 {
					displayRoute = displayRoute[:22] + "..."
				}

				fmt.Printf(" %-6s  %-25s  %-12d  %-10.2f  %-12.2f ms\n",
					rKey.method, displayRoute, currVal.requests, rps, avgLatencyMs)
			}
		}
		fmt.Printf("\x1b[36m========================================================================\x1b[0m\n")
		fmt.Println(" Press Ctrl+C to exit.")

		// Save state for next iteration
		prevMetrics = currMetrics
		prevTime = currTime

		time.Sleep(1 * time.Second)
	}
}

// Parses labels like name{label1="val1",label2="val2"}
func parseLabels(metricKey string) map[string]string {
	labels := make(map[string]string)
	start := strings.Index(metricKey, "{")
	end := strings.Index(metricKey, "}")
	if start == -1 || end == -1 || start >= end {
		return labels
	}
	labelStr := metricKey[start+1 : end]
	pairs := strings.Split(labelStr, ",")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			k := strings.TrimSpace(kv[0])
			v := strings.Trim(strings.TrimSpace(kv[1]), "\"")
			labels[k] = v
		}
	}
	return labels
}
