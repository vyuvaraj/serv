package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/client"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/inspector"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/otel"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/server"

	"gopkg.in/yaml.v3"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "server":
		runServer()
	case "client":
		runClient()
	case "version":
		fmt.Printf("ServTunnel v%s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runServer() {
	// Initialize OpenTelemetry.
	otel.Init()

	addr := getEnvOrDefault("SERVTUNNEL_ADDR", ":8443")
	baseDomain := getEnvOrDefault("SERVTUNNEL_DOMAIN", "localhost")
	inspectSize := 100

	// Parse flags from remaining args.
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--port", "-p":
			if i+1 < len(os.Args) {
				addr = ":" + os.Args[i+1]
				i++
			}
		case "--domain", "-d":
			if i+1 < len(os.Args) {
				baseDomain = os.Args[i+1]
				i++
			}
		}
	}

	insp := inspector.New(inspectSize)
	srv := server.NewServer(addr, baseDomain, insp)

	log.Println("Starting ServTunnel relay server...")

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Graceful shutdown.
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("ServTunnel: Shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("ServTunnel: Forced shutdown: %v", err)
	}
	log.Println("ServTunnel: Shutdown complete")
}

type TunnelConfigItem struct {
	Port         string `yaml:"port"`
	Subdomain    string `yaml:"subdomain"`
	CustomDomain string `yaml:"custom_domain"`
	Token        string `yaml:"token"`
	InspectPort  string `yaml:"inspect_port"`
	ShareAuth    string `yaml:"share_auth"`
	Throttle     string `yaml:"throttle"`
}

type TunnelConfigFile struct {
	Relay   string             `yaml:"relay"`
	Token   string             `yaml:"token"`
	Tunnels []TunnelConfigItem `yaml:"tunnels"`
}

func tryLoadConfig() (*TunnelConfigFile, string) {
	paths := []string{".serv/tunnel.yaml", "servtunnel.yaml", "tunnel.yaml"}
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			var cfg TunnelConfigFile
			if err := yaml.Unmarshal(data, &cfg); err == nil {
				return &cfg, p
			}
		}
	}
	return nil, ""
}

func runClient() {
	var cfgFile *TunnelConfigFile
	var cfgPath string

	if len(os.Args) >= 4 && os.Args[2] == "--config" {
		path := os.Args[3]
		if data, err := os.ReadFile(path); err == nil {
			var cfg TunnelConfigFile
			if err := yaml.Unmarshal(data, &cfg); err == nil {
				cfgFile = &cfg
				cfgPath = path
			} else {
				log.Fatalf("Failed to parse config file: %v", err)
			}
		} else {
			log.Fatalf("Failed to read config file %s: %v", path, err)
		}
	} else if len(os.Args) < 3 {
		cfgFile, cfgPath = tryLoadConfig()
		if cfgFile == nil {
			fmt.Fprintf(os.Stderr, "Usage: servtunnel client <local-port> [options]\n")
			fmt.Fprintf(os.Stderr, "       servtunnel client --config <config-path>\n")
			fmt.Fprintf(os.Stderr, "\nOptions:\n")
			fmt.Fprintf(os.Stderr, "  --relay <url>       Relay server WebSocket URL (default: ws://localhost:8443/ws/connect)\n")
			fmt.Fprintf(os.Stderr, "  --subdomain <name>  Requested subdomain (default: auto-generated)\n")
			fmt.Fprintf(os.Stderr, "  --token <token>     Authentication token\n")
			fmt.Fprintf(os.Stderr, "  --inspect-port <p>  Local inspection web UI port (default: 4040, use 0 or empty to disable)\n")
			os.Exit(1)
		}
	} else {
		// Also check default config if port is not a config switch
		if !strings.HasPrefix(os.Args[2], "-") {
			cfgFile, cfgPath = tryLoadConfig()
		}
	}

	if cfgFile != nil {
		log.Printf("Loaded tunnel config from %s", cfgPath)
		relayURL := cfgFile.Relay
		if relayURL == "" {
			relayURL = getEnvOrDefault("SERVTUNNEL_RELAY", "ws://localhost:8443/ws/connect")
		}
		globalToken := cfgFile.Token
		if globalToken == "" {
			globalToken = getEnvOrDefault("SERVTUNNEL_TOKEN", "")
		}

		var wg sync.WaitGroup
		for i, t := range cfgFile.Tunnels {
			wg.Add(1)
			currInspectPort := t.InspectPort
			if currInspectPort == "" {
				if i == 0 {
					currInspectPort = "4040"
				} else {
					currInspectPort = "0"
				}
			}
			tToken := t.Token
			if tToken == "" {
				tToken = globalToken
			}

			go func(tc TunnelConfigItem, insp string, tok string) {
				defer wg.Done()
				localAddr := "localhost:" + tc.Port
				c := client.NewClient(localAddr, relayURL, tc.Subdomain, tc.CustomDomain, tok, insp, tc.ShareAuth)
				if tc.Throttle != "" {
					c.WithThrottle(tc.Throttle)
				}
				if err := c.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "Tunnel error on port %s (subdomain: %s): %v\n", tc.Port, tc.Subdomain, err)
				}
			}(t, currInspectPort, tToken)
		}
		wg.Wait()
		return
	}

	localPort := os.Args[2]
	relayURL := getEnvOrDefault("SERVTUNNEL_RELAY", "ws://localhost:8443/ws/connect")
	subdomain := ""
	customDomain := ""
	token := getEnvOrDefault("SERVTUNNEL_TOKEN", "")
	inspectPort := "4040"
	shareAuth := ""

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--relay", "-r":
			if i+1 < len(os.Args) {
				relayURL = os.Args[i+1]
				i++
			}
		case "--subdomain", "-s":
			if i+1 < len(os.Args) {
				subdomain = os.Args[i+1]
				i++
			}
		case "--custom-domain", "-c":
			if i+1 < len(os.Args) {
				customDomain = os.Args[i+1]
				i++
			}
		case "--token", "-t":
			if i+1 < len(os.Args) {
				token = os.Args[i+1]
				i++
			}
		case "--inspect-port", "-i":
			if i+1 < len(os.Args) {
				inspectPort = os.Args[i+1]
				i++
			}
		case "--share-auth", "-a":
			if i+1 < len(os.Args) {
				shareAuth = os.Args[i+1]
				i++
			}
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
			if gitSub := getGitBranchSubdomain(); gitSub != "" {
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
		currInspectPort := inspectPort
		if i > 0 {
			currInspectPort = "0"
		}
		go func(port, sub, insp string) {
			defer wg.Done()
			localAddr := "localhost:" + port
			c := client.NewClient(localAddr, relayURL, sub, customDomain, token, insp, shareAuth)
			if err := c.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Tunnel error on port %s (subdomain: %s): %v\n", port, sub, err)
			}
		}(tc.port, tc.subdomain, currInspectPort)
	}
	wg.Wait()
}

func getGitBranchSubdomain() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return ""
	}

	return sanitizeBranchName(branch)
}

func sanitizeBranchName(branch string) string {
	// Lowercase
	branch = strings.ToLower(branch)
	// Replace non-alphanumeric character sequences with a single hyphen
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	sanitized := reg.ReplaceAllString(branch, "-")
	// Trim leading/trailing hyphens
	sanitized = strings.Trim(sanitized, "-")

	return sanitized
}



func printUsage() {
	fmt.Printf("ServTunnel v%s — Secure dev tunneling for the Serv ecosystem\n\n", version)
	fmt.Println("Usage: servtunnel <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  server    Start the tunnel relay server")
	fmt.Println("  client    Connect a local service to the relay")
	fmt.Println("  version   Print version information")
	fmt.Println("  help      Show this help message")
	fmt.Println()
	fmt.Println("Server options:")
	fmt.Println("  --port, -p <port>      Listen port (default: 8443)")
	fmt.Println("  --domain, -d <domain>  Base domain for subdomains (default: localhost)")
	fmt.Println()
	fmt.Println("Client options:")
	fmt.Println("  servtunnel client <local-port> [options]")
	fmt.Println("  --relay, -r <url>         Relay WebSocket URL (default: ws://localhost:8443/ws/connect)")
	fmt.Println("  --subdomain, -s <name>    Requested subdomain (default: auto-generated)")
	fmt.Println("  --custom-domain, -c <dom> Requested custom domain mapping (optional)")
	fmt.Println("  --token, -t <token>       Authentication token")
	fmt.Println("  --inspect-port, -i <port> Local inspection web UI port (default: 4040)")
	fmt.Println("  --share-auth, -a <usr:pwd> Basic authentication to protect public tunnel (optional)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  servtunnel server --port 8443 --domain servverse.net")
	fmt.Println("  servtunnel client 8080 --relay ws://relay.servverse.net:8443/ws/connect --subdomain myapp")
	fmt.Println()
	fmt.Println("Environment variables:")
	fmt.Println("  SERVTUNNEL_ADDR    Server listen address (default: :8443)")
	fmt.Println("  SERVTUNNEL_DOMAIN  Base domain (default: localhost)")
	fmt.Println("  SERVTUNNEL_RELAY   Client relay URL (default: ws://localhost:8443/ws/connect)")
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
