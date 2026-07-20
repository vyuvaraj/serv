package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/client"
)

func runTunnelCmd() {
	if len(os.Args) >= 3 && os.Args[2] == "inspect" {
		runTunnelInspect()
		return
	}

	tunnelCmd := flag.NewFlagSet("tunnel", flag.ExitOnError)
	relayFlag := tunnelCmd.String("relay", "ws://localhost:8443/ws/connect", "Relay server WebSocket URL")
	subdomainFlag := tunnelCmd.String("subdomain", "", "Requested subdomain")
	customDomainFlag := tunnelCmd.String("custom-domain", "", "Requested custom domain mapping")
	tokenFlag := tunnelCmd.String("token", "", "Authentication token")
	inspectPortFlag := tunnelCmd.String("inspect-port", "4040", "Local inspection web UI port (use 0 or empty to disable)")
	shareAuthFlag := tunnelCmd.String("share-auth", "", "Basic authentication credentials (usr:pwd) to protect the public tunnel")

	// Allow short flags as well
	tunnelCmd.StringVar(relayFlag, "r", "ws://localhost:8443/ws/connect", "Relay server WebSocket URL")
	tunnelCmd.StringVar(subdomainFlag, "s", "", "Requested subdomain")
	tunnelCmd.StringVar(customDomainFlag, "c", "", "Requested custom domain mapping")
	tunnelCmd.StringVar(tokenFlag, "t", "", "Authentication token")
	tunnelCmd.StringVar(inspectPortFlag, "i", "4040", "Local inspection web UI port")
	tunnelCmd.StringVar(shareAuthFlag, "a", "", "Basic authentication credentials (usr:pwd)")

	// Parse local port (mandatory argument)
	if len(os.Args) < 3 {
		fmt.Println("Usage: serv tunnel <local-port> [options]")
		fmt.Println("\nOptions:")
		tunnelCmd.PrintDefaults()
		os.Exit(1)
	}

	localPort := os.Args[2]
	// Parse remaining flags starting from os.Args[3:]
	if err := tunnelCmd.Parse(os.Args[3:]); err != nil {
		fmt.Printf("Error parsing arguments: %v\n", err)
		os.Exit(1)
	}

	subdomain := *subdomainFlag
	customDomain := *customDomainFlag

	// If no subdomain, try to get git branch subdomain
	if subdomain == "" {
		if gitSub := getGitBranchSubdomainForServ(); gitSub != "" {
			subdomain = gitSub
			log.Printf("No subdomain specified. Auto-detected Git branch: %s", subdomain)
		}
	}

	var tunnelConfigs []struct {
		port      string
		subdomain string
	}

	parts := strings.Split(localPort, ",")
	for _, p := range parts {
		subParts := strings.Split(p, ":")
		portVal := subParts[0]
		subVal := ""
		if len(subParts) > 1 {
			subVal = subParts[1]
		} else {
			subVal = subdomain
		}

		if subVal == "" {
			if gitSub := getGitBranchSubdomainForServ(); gitSub != "" {
				subVal = gitSub
				log.Printf("No subdomain specified for port %s. Auto-detected Git branch: %s", portVal, subVal)
			}
		}
		tunnelConfigs = append(tunnelConfigs, struct {
			port      string
			subdomain string
		}{port: portVal, subdomain: subVal})
	}

	var wg sync.WaitGroup
	for i, tc := range tunnelConfigs {
		wg.Add(1)
		currInspectPort := *inspectPortFlag
		if i > 0 {
			currInspectPort = "0"
		}
		go func(port, sub, insp string) {
			defer wg.Done()
			localAddr := "localhost:" + port
			c := client.NewClient(localAddr, *relayFlag, sub, customDomain, *tokenFlag, insp, *shareAuthFlag)
			if err := c.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Tunnel error on port %s (subdomain: %s): %v\n", port, sub, err)
			}
		}(tc.port, tc.subdomain, currInspectPort)
	}
	wg.Wait()
}

func getGitBranchSubdomainForServ() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return ""
	}

	// Lowercase
	branch = strings.ToLower(branch)
	// Replace non-alphanumeric character sequences with a single hyphen
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	sanitized := reg.ReplaceAllString(branch, "-")
	// Trim leading/trailing hyphens
	sanitized = strings.Trim(sanitized, "-")

	return sanitized
}

func runTunnelInspect() {
	relayHost := "http://localhost:8443"
	if envHost := os.Getenv("SERVTUNNEL_URL"); envHost != "" {
		relayHost = envHost
	}

	for i := 3; i < len(os.Args); i++ {
		if (os.Args[i] == "--relay" || os.Args[i] == "-r" || os.Args[i] == "--host") && i+1 < len(os.Args) {
			relayHost = os.Args[i+1]
			relayHost = strings.Replace(relayHost, "ws://", "http://", 1)
			relayHost = strings.Replace(relayHost, "wss://", "https://", 1)
			relayHost = strings.TrimSuffix(relayHost, "/ws/connect")
			i++
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}

	tunnelsURL := fmt.Sprintf("%s/api/tunnels", strings.TrimSuffix(relayHost, "/"))
	tReq, _ := http.NewRequest("GET", tunnelsURL, nil)
	authToken := os.Getenv("SERVTUNNEL_TOKEN")
	if authToken != "" {
		tReq.Header.Set("Authorization", "Bearer "+authToken)
	}
	tResp, err := client.Do(tReq)
	if err != nil {
		fmt.Printf("Failed to connect to ServTunnel relay at %s: %v\n", relayHost, err)
		os.Exit(1)
	}
	defer tResp.Body.Close()

	if tResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tResp.Body)
		fmt.Printf("ServTunnel relay returned error status %d: %s\n", tResp.StatusCode, string(body))
		os.Exit(1)
	}

	var tData struct {
		Tunnels []map[string]interface{} `json:"tunnels"`
		Count   int                      `json:"count"`
	}
	if err := json.NewDecoder(tResp.Body).Decode(&tData); err != nil {
		fmt.Printf("Failed to parse tunnels response: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== ServTunnel Active Connections ===")
	if len(tData.Tunnels) == 0 {
		fmt.Println("  No active tunnel connections.")
	} else {
		for _, t := range tData.Tunnels {
			sub := t["subdomain"]
			pub := t["public_url"]
			read := t["bytes_read"]
			write := t["bytes_written"]
			fmt.Printf("  Subdomain:  %s\n", sub)
			fmt.Printf("  Public URL: %s\n", pub)
			fmt.Printf("  Throughput: Read: %v bytes | Written: %v bytes\n", read, write)
			fmt.Println("  ---------------------------------")
		}
	}

	inspectURL := fmt.Sprintf("%s/api/inspect?limit=10", strings.TrimSuffix(relayHost, "/"))
	iReq, _ := http.NewRequest("GET", inspectURL, nil)
	if authToken != "" {
		iReq.Header.Set("Authorization", "Bearer "+authToken)
	}
	iResp, err := client.Do(iReq)
	if err != nil {
		fmt.Printf("Failed to fetch inspection logs: %v\n", err)
		return
	}
	defer iResp.Body.Close()

	if iResp.StatusCode != http.StatusOK {
		return
	}

	var iData struct {
		Entries []map[string]interface{} `json:"entries"`
		Total   int64                    `json:"total"`
	}
	if err := json.NewDecoder(iResp.Body).Decode(&iData); err != nil {
		return
	}

	fmt.Println("\n=== Recent Tunnel Requests ===")
	if len(iData.Entries) == 0 {
		fmt.Println("  No requests captured yet.")
	} else {
		fmt.Printf("  %-6s %-10s %-30s %-8s %-10s\n", "ID", "METHOD", "PATH", "STATUS", "LATENCY")
		fmt.Println("  " + strings.Repeat("-", 70))
		for idx := len(iData.Entries) - 1; idx >= 0; idx-- {
			e := iData.Entries[idx]
			fmt.Printf("  %-6v %-10v %-30v %-8v %-10vms\n",
				e["id"], e["method"], e["path"], e["status_code"], e["latency_ms"])
		}
		fmt.Printf("\n  Total requests captured: %d\n", iData.Total)
	}
}
