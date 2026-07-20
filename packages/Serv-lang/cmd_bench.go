package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runBench runs a built-in load test against a compiled Serv service.
// It reads route declarations from the .srv source, generates k6-style
// scenarios in-process, then fires HTTP requests and reports results.
//
// Usage: serv bench <file.srv> [--host <url>] [--rps <n>] [--duration <s>] [--output <json|text>]
func runBench() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: serv bench <file.srv> [--host <url>] [--rps <n>] [--duration <s>] [--output <json|text>]")
		fmt.Println("")
		fmt.Println("Examples:")
		fmt.Println("  serv bench main.srv")
		fmt.Println("  serv bench main.srv --host http://localhost:8080 --rps 100 --duration 30")
		fmt.Println("  serv bench main.srv --output json > bench-results.json")
		os.Exit(1)
	}

	srcFile := os.Args[2]
	host := "http://localhost:8080"
	rps := 50
	duration := 10 // seconds
	outputFmt := "text"

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--host", "-host":
			if i+1 < len(os.Args) {
				host = os.Args[i+1]
				i++
			}
		case "--rps", "-rps":
			if i+1 < len(os.Args) {
				fmt.Sscanf(os.Args[i+1], "%d", &rps)
				i++
			}
		case "--duration", "-duration", "-d":
			if i+1 < len(os.Args) {
				fmt.Sscanf(os.Args[i+1], "%d", &duration)
				i++
			}
		case "--output", "-output", "-o":
			if i+1 < len(os.Args) {
				outputFmt = os.Args[i+1]
				i++
			}
		}
	}

	// Parse routes from .srv source
	routes, err := extractBenchRoutes(srcFile)
	if err != nil {
		fmt.Printf("Failed to parse %s: %v\n", srcFile, err)
		os.Exit(1)
	}
	if len(routes) == 0 {
		fmt.Printf("No GET routes found in %s. Benching /health instead.\n", srcFile)
		routes = []benchRoute{{Method: "GET", Path: "/health"}}
	}

	if outputFmt == "text" {
		fmt.Printf("🔥 Serv Bench — %s\n", srcFile)
		fmt.Printf("   Host:     %s\n", host)
		fmt.Printf("   Routes:   %d\n", len(routes))
		fmt.Printf("   RPS:      %d\n", rps)
		fmt.Printf("   Duration: %ds\n\n", duration)
	}

	results := runBenchLoad(host, routes, rps, duration)
	printBenchResults(results, outputFmt)
}

type benchRoute struct {
	Method string
	Path   string
}

type benchResult struct {
	Route      string
	Requests   int
	Successes  int
	Failures   int
	AvgLatency time.Duration
	P99Latency time.Duration
	RPS        float64
}

// extractBenchRoutes scans a .srv file for route declarations.
func extractBenchRoutes(srcFile string) ([]benchRoute, error) {
	data, err := os.ReadFile(srcFile)
	if err != nil {
		return nil, err
	}

	var routes []benchRoute
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Match: route "GET" "/path" or route "POST" "/path"
		if !strings.HasPrefix(line, "route ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		method := strings.Trim(parts[1], `"`)
		path := strings.Trim(parts[2], `"(`)
		// Only bench idempotent routes by default
		if method == "GET" || method == "HEAD" {
			routes = append(routes, benchRoute{Method: method, Path: path})
		}
	}
	return routes, nil
}

// runBenchLoad fires requests against all routes and collects latency stats.
func runBenchLoad(host string, routes []benchRoute, rps, durationSec int) []benchResult {
	client := &http.Client{Timeout: 5 * time.Second}
	results := make([]benchResult, len(routes))

	totalRequests := rps * durationSec
	perRoute := totalRequests / len(routes)
	if perRoute < 1 {
		perRoute = 1
	}

	for i, route := range routes {
		url := strings.TrimSuffix(host, "/") + route.Path
		// Replace path params like :id with "1"
		url = strings.NewReplacer(":id", "1", ":name", "test", ":slug", "bench").Replace(url)

		var latencies []time.Duration
		successes := 0
		failures := 0

		for j := 0; j < perRoute; j++ {
			start := time.Now()
			resp, err := client.Get(url) //nolint:gosec
			elapsed := time.Since(start)
			latencies = append(latencies, elapsed)
			if err != nil || resp.StatusCode >= 500 {
				failures++
			} else {
				successes++
			}
			if err == nil {
				resp.Body.Close()
			}
		}

		avg, p99 := calcLatencyStats(latencies)
		results[i] = benchResult{
			Route:      route.Method + " " + route.Path,
			Requests:   perRoute,
			Successes:  successes,
			Failures:   failures,
			AvgLatency: avg,
			P99Latency: p99,
			RPS:        float64(perRoute) / float64(durationSec),
		}
	}
	return results
}

func calcLatencyStats(latencies []time.Duration) (avg, p99 time.Duration) {
	if len(latencies) == 0 {
		return 0, 0
	}
	var total time.Duration
	for _, l := range latencies {
		total += l
	}
	avg = total / time.Duration(len(latencies))

	// Simple p99: sort and take 99th percentile
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	p99idx := int(float64(len(sorted)) * 0.99)
	if p99idx >= len(sorted) {
		p99idx = len(sorted) - 1
	}
	p99 = sorted[p99idx]
	return
}

func printBenchResults(results []benchResult, outputFmt string) {
	if outputFmt == "json" {
		fmt.Print("[")
		for i, r := range results {
			if i > 0 {
				fmt.Print(",")
			}
			fmt.Printf(`{"route":%q,"requests":%d,"successes":%d,"failures":%d,"avg_ms":%.2f,"p99_ms":%.2f,"rps":%.1f}`,
				r.Route, r.Requests, r.Successes, r.Failures,
				float64(r.AvgLatency.Microseconds())/1000,
				float64(r.P99Latency.Microseconds())/1000,
				r.RPS,
			)
		}
		fmt.Println("]")
		return
	}

	// Text output
	fmt.Printf("%-35s  %8s  %8s  %8s  %10s  %10s  %8s\n",
		"ROUTE", "REQS", "OK", "FAIL", "AVG", "P99", "RPS")
	fmt.Println(strings.Repeat("─", 95))
	for _, r := range results {
		status := "✅"
		if r.Failures > 0 {
			status = "⚠️ "
		}
		fmt.Printf("%s %-33s  %8d  %8d  %8d  %10s  %10s  %8.1f\n",
			status, r.Route, r.Requests, r.Successes, r.Failures,
			fmtDuration(r.AvgLatency), fmtDuration(r.P99Latency), r.RPS)
	}
	fmt.Println("")

	total := 0
	for _, r := range results {
		total += r.Requests
	}
	fmt.Printf("Total requests: %d\n", total)
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}
