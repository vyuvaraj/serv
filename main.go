package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type LockRequest struct {
	Key          string `json:"key"`
	Owner        string `json:"owner"`
	ClientID     string `json:"client_id"`
	FencingToken int64  `json:"fencing_token"`
	Duration     int    `json:"duration_ms"` // Lease TTL in milliseconds
	WaitTime     int    `json:"wait_ms"`     // Optional block/wait timeout in milliseconds
	Mode         string `json:"mode"`        // "shared" or "exclusive"
	Priority     int    `json:"priority"`
}

type LockResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Lock    any    `json:"lock,omitempty"`
}

func main() {
	// Root flags
	serverURL := flag.String("url", "", "ServLock server URL")
	apiKey := flag.String("api-key", "", "API Key for authorization")
	tenant := flag.String("tenant", "default", "Tenant ID for request")

	// Command flags
	key := flag.String("key", "", "Lock key name")
	owner := flag.String("owner", "cli-user", "Lock owner name")
	clientID := flag.String("client-id", "cli-client", "Lock Client ID")
	fencingToken := flag.Int64("fencing-token", 0, "Fencing token for release/renew")
	duration := flag.Int("ttl", 10000, "Lease duration in milliseconds")
	wait := flag.Int("wait", 0, "Wait time in milliseconds for blocking lock acquisition")
	mode := flag.String("mode", "exclusive", "Lock mode (exclusive or shared)")
	priority := flag.Int("priority", 0, "Lock request priority")

	flag.Parse()

	// Default Server URL from env if not set
	if *serverURL == "" {
		*serverURL = os.Getenv("SERVLOCK_URL")
		if *serverURL == "" {
			*serverURL = "http://localhost:8089"
		}
	}

	// Default API Key from env if not set
	if *apiKey == "" {
		*apiKey = os.Getenv("SERVLOCK_API_KEY")
	}

	args := flag.Args()
	if len(args) < 1 {
		printUsage()
		os.Exit(1)
	}

	action := args[0]
	client := &http.Client{Timeout: 30 * time.Second}

	switch action {
	case "acquire":
		if *key == "" {
			log.Fatal("Error: --key is required for acquire")
		}
		reqBody := LockRequest{
			Key:      *key,
			Owner:    *owner,
			ClientID: *clientID,
			Duration: *duration,
			WaitTime: *wait,
			Mode:     *mode,
			Priority: *priority,
		}
		respBytes, code, err := sendRequest(client, http.MethodPost, *serverURL+"/api/v1/locks/acquire", reqBody, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "release":
		if *key == "" {
			log.Fatal("Error: --key is required for release")
		}
		reqBody := LockRequest{
			Key:          *key,
			Owner:        *owner,
			FencingToken: *fencingToken,
		}
		respBytes, code, err := sendRequest(client, http.MethodPost, *serverURL+"/api/v1/locks/release", reqBody, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "renew":
		if *key == "" {
			log.Fatal("Error: --key is required for renew")
		}
		reqBody := LockRequest{
			Key:          *key,
			Owner:        *owner,
			FencingToken: *fencingToken,
			Duration:     *duration,
		}
		respBytes, code, err := sendRequest(client, http.MethodPost, *serverURL+"/api/v1/locks/renew", reqBody, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "status", "list":
		respBytes, code, err := sendRequest(client, http.MethodGet, *serverURL+"/api/v1/locks/observability", nil, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "health":
		// Health check readyz
		resp, err := client.Get(*serverURL + "/readyz")
		if err != nil {
			log.Fatalf("Health check failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("Status: %d\nResponse: %s\n", resp.StatusCode, string(body))

	default:
		fmt.Printf("Unknown command: %s\n", action)
		printUsage()
		os.Exit(1)
	}
}

func sendRequest(client *http.Client, method, url string, bodyObj any, tenant, apiKey string) ([]byte, int, error) {
	var bodyReader io.Reader
	if bodyObj != nil {
		data, err := json.Marshal(bodyObj)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	return respBytes, resp.StatusCode, err
}

func printUsage() {
	fmt.Println(`servlockctl is a command-line client for ServLock.

Usage:
  servlockctl [command] [flags]

Commands:
  acquire       Acquire a new lock
  release       Release an active lock
  renew         Renew a lock lease
  status        Get observability list of active locks
  health        Verify ServLock service health

Flags:
  --url            ServLock server URL (default: http://localhost:8089 or SERVLOCK_URL env)
  --api-key        API key for authentication (or SERVLOCK_API_KEY env)
  --tenant         Tenant ID for isolation (default: default)
  --key            Lock key name (required for acquire/release/renew)
  --owner          Lock owner name (default: cli-user)
  --client-id      Client ID (default: cli-client)
  --fencing-token  Fencing token received upon acquisition (required for release/renew)
  --ttl            Lease duration in milliseconds (default: 10000)
  --wait           Max wait time in milliseconds for blocking acquisition
  --mode           Lock mode: 'exclusive' or 'shared' (default: exclusive)
  --priority       Lock priority (default: 0)`)
}
