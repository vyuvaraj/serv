// Package main implements the `servmesh inspect` CLI subcommand.
// It queries the ServMesh registry /api/topology endpoint and renders a
// human-readable table of all registered service instances annotated with
// their real-time health metrics (latency, error rate, state).
//
// Usage:
//
//	go run ./cmd/inspect/ [--registry <url>] [--service <name>] [--watch]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// TopologyEntry mirrors registry.TopologyEntry for JSON decoding.
type TopologyEntry struct {
	Service      string    `json:"service"`
	Address      string    `json:"address"`
	Version      string    `json:"version"`
	Region       string    `json:"region"`
	Weight       int       `json:"weight"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
	ErrorRate    float64   `json:"error_rate"`
	State        string    `json:"state"`
	LastSeen     time.Time `json:"last_seen"`
	ReportedAt   time.Time `json:"reported_at"`
}

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

func main() {
	registry := flag.String("registry", "http://localhost:8089", "ServMesh registry URL")
	service := flag.String("service", "", "Filter by service name")
	watch := flag.Bool("watch", false, "Refresh every 2 seconds (live mode)")
	interval := flag.Duration("interval", 2*time.Second, "Refresh interval when --watch is set")
	flag.Parse()

	if *watch {
		for {
			clearScreen()
			if err := render(*registry, *service); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			fmt.Printf("\n%sRefreshing every %s — press Ctrl+C to exit%s\n", colorDim, *interval, colorReset)
			time.Sleep(*interval)
		}
	} else {
		if err := render(*registry, *service); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func render(registryURL, serviceFilter string) error {
	entries, err := fetchTopology(registryURL, serviceFilter)
	if err != nil {
		return err
	}

	now := time.Now()
	fmt.Printf("%s%s ServMesh Topology Inspector%s — %s\n",
		colorBold, colorCyan, colorReset, now.Format("2006-01-02 15:04:05"))
	fmt.Printf("Registry: %s%s%s\n\n", colorDim, registryURL, colorReset)

	if len(entries) == 0 {
		fmt.Println("No services registered.")
		return nil
	}

	// Column widths
	const (
		wSvc  = 24
		wAddr = 32
		wVer  = 9
		wLat  = 10
		wErr  = 10
		wState = 10
		wSeen = 14
	)

	// Header
	fmt.Printf("%s%-*s %-*s %-*s %*s %*s %-*s %-*s%s\n",
		colorBold,
		wSvc, "SERVICE",
		wAddr, "ADDRESS",
		wVer, "VERSION",
		wLat, "LATENCY",
		wErr, "ERR RATE",
		wState, "STATE",
		wSeen, "LAST SEEN",
		colorReset,
	)
	fmt.Println(strings.Repeat("─", wSvc+wAddr+wVer+wLat+wErr+wState+wSeen+7))

	// Group by service for cleaner output
	services := make(map[string][]TopologyEntry)
	order := []string{}
	for _, e := range entries {
		if _, seen := services[e.Service]; !seen {
			order = append(order, e.Service)
		}
		services[e.Service] = append(services[e.Service], e)
	}

	for _, svc := range order {
		list := services[svc]
		for i, e := range list {
			svcLabel := ""
			if i == 0 {
				svcLabel = truncate(e.Service, wSvc)
			}

			// Version
			ver := e.Version
			if ver == "" {
				ver = "-"
			}

			// Latency
			latStr := "-"
			if e.AvgLatencyMs > 0 {
				latStr = fmt.Sprintf("%.1fms", e.AvgLatencyMs)
			}

			// Error rate
			errStr := "-"
			if !e.ReportedAt.IsZero() {
				errStr = fmt.Sprintf("%.1f%%", e.ErrorRate*100)
			}

			// State color
			stateColor := colorDim
			switch e.State {
			case "healthy":
				stateColor = colorGreen
			case "degraded":
				stateColor = colorYellow
			}

			// Last seen
			seenAgo := now.Sub(e.LastSeen).Round(time.Second)
			seenStr := fmt.Sprintf("%s ago", seenAgo)

			fmt.Printf("%-*s %-*s %s%-*s%s %*s %*s %s%-*s%s %-*s\n",
				wSvc, svcLabel,
				wAddr, truncate(e.Address, wAddr),
				colorCyan, wVer, ver, colorReset,
				wLat, latStr,
				wErr, errStr,
				stateColor, wState, e.State, colorReset,
				wSeen, seenStr,
			)
		}
		// Blank row between service groups
		fmt.Println()
	}

	fmt.Printf("Total: %d instance(s) across %d service(s)\n", len(entries), len(order))
	return nil
}

func fetchTopology(registryURL, serviceFilter string) ([]TopologyEntry, error) {
	u, err := url.Parse(registryURL + "/api/topology")
	if err != nil {
		return nil, fmt.Errorf("invalid registry URL: %w", err)
	}
	if serviceFilter != "" {
		q := u.Query()
		q.Set("service", serviceFilter)
		u.RawQuery = q.Encode()
	}

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to reach registry at %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("registry returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var entries []TopologyEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode topology: %w", err)
	}
	return entries, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
