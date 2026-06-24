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
	"time"

	"servtunnel/pkg/client"
	"servtunnel/pkg/inspector"
	"servtunnel/pkg/otel"
	"servtunnel/pkg/server"
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

func runClient() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: servtunnel client <local-port> [options]\n")
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		fmt.Fprintf(os.Stderr, "  --relay <url>       Relay server WebSocket URL (default: ws://localhost:8443/ws/connect)\n")
		fmt.Fprintf(os.Stderr, "  --subdomain <name>  Requested subdomain (default: auto-generated)\n")
		fmt.Fprintf(os.Stderr, "  --token <token>     Authentication token\n")
		os.Exit(1)
	}

	localPort := os.Args[2]
	relayURL := getEnvOrDefault("SERVTUNNEL_RELAY", "ws://localhost:8443/ws/connect")
	subdomain := ""
	token := getEnvOrDefault("SERVTUNNEL_TOKEN", "")

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
		case "--token", "-t":
			if i+1 < len(os.Args) {
				token = os.Args[i+1]
				i++
			}
		}
	}

	if subdomain == "" {
		if gitSub := getGitBranchSubdomain(); gitSub != "" {
			subdomain = gitSub
			log.Printf("No subdomain specified. Auto-detected Git branch: %s", subdomain)
		}
	}

	localAddr := "localhost:" + localPort
	c := client.NewClient(localAddr, relayURL, subdomain, token)

	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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
	fmt.Println("  --token, -t <token>       Authentication token")
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
