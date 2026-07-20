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
	"os/exec"
	"strings"
	"time"
)

type SecretRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type SecretResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ListResponse struct {
	Keys []string `json:"keys"`
}

type RotateRequest struct {
	NewMasterKey string `json:"new_master_key"`
}

type RollbackRequest struct {
	Key string `json:"key"`
}

func main() {
	// Root flags
	serverURL := flag.String("url", "", "ServSecret server URL")
	apiKey := flag.String("api-key", "", "API Key for authentication")
	tenant := flag.String("tenant", "default", "Tenant ID for request isolation")

	// Action flags
	key := flag.String("key", "", "Secret key")
	value := flag.String("value", "", "Secret value for set action")
	newMasterKey := flag.String("new-key", "", "New master key (hex) for rotation")
	cmdToRun := flag.String("cmd", "", "Command to run with injected secrets (only for 'run')")

	flag.Parse()

	// Default Server URL
	if *serverURL == "" {
		*serverURL = os.Getenv("SERVSECRET_URL")
		if *serverURL == "" {
			*serverURL = "http://localhost:8091"
		}
	}

	// Default API Key
	if *apiKey == "" {
		*apiKey = os.Getenv("SERVSECRET_API_KEY")
	}

	args := flag.Args()
	if len(args) < 1 {
		printUsage()
		os.Exit(1)
	}

	action := args[0]
	client := &http.Client{Timeout: 10 * time.Second}

	switch action {
	case "set":
		if *key == "" || *value == "" {
			log.Fatal("Error: --key and --value are required for set")
		}
		reqBody := SecretRequest{Key: *key, Value: *value}
		respBytes, code, err := sendRequest(client, http.MethodPost, *serverURL+"/api/v1/secrets", reqBody, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "get":
		if *key == "" {
			log.Fatal("Error: --key is required for get")
		}
		respBytes, code, err := sendRequest(client, http.MethodGet, *serverURL+"/api/v1/secrets/"+*key, nil, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "delete":
		if *key == "" {
			log.Fatal("Error: --key is required for delete")
		}
		respBytes, code, err := sendRequest(client, http.MethodDelete, *serverURL+"/api/v1/secrets/"+*key, nil, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "list":
		respBytes, code, err := sendRequest(client, http.MethodGet, *serverURL+"/api/v1/secrets", nil, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "rotate":
		if *newMasterKey == "" {
			log.Fatal("Error: --new-key is required for rotate")
		}
		reqBody := RotateRequest{NewMasterKey: *newMasterKey}
		respBytes, code, err := sendRequest(client, http.MethodPost, *serverURL+"/api/v1/secrets/rotate", reqBody, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "rollback":
		if *key == "" {
			log.Fatal("Error: --key is required for rollback")
		}
		reqBody := RollbackRequest{Key: *key}
		respBytes, code, err := sendRequest(client, http.MethodPost, *serverURL+"/api/v1/secrets/rollback", reqBody, *tenant, *apiKey)
		if err != nil {
			log.Fatalf("Request failed: %v", err)
		}
		fmt.Printf("HTTP Status: %d\n", code)
		fmt.Printf("Response: %s\n", string(respBytes))

	case "run":
		if *cmdToRun == "" {
			log.Fatal("Error: --cmd is required for run")
		}
		runEnvInjector(*cmdToRun, *serverURL, *tenant, *apiKey, client)

	case "health":
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

func runEnvInjector(command, serverURL, tenant, apiKey string, client *http.Client) {
	// 1. Fetch list of secrets keys
	respBytes, code, err := sendRequest(client, http.MethodGet, serverURL+"/api/v1/secrets", nil, tenant, apiKey)
	if err != nil {
		log.Fatalf("failed to query secrets: %v", err)
	}
	if code != http.StatusOK {
		log.Fatalf("failed to retrieve secrets list: %d, response: %s", code, string(respBytes))
	}

	var listResp ListResponse
	if err := json.Unmarshal(respBytes, &listResp); err != nil {
		log.Fatalf("failed to parse secret keys list: %v", err)
	}

	// 2. Query each secret and build env variables slice
	var secretEnvs []string
	for _, key := range listResp.Keys {
		getURL := fmt.Sprintf("%s/api/v1/secrets/%s", serverURL, key)
		respGet, getCode, err := sendRequest(client, http.MethodGet, getURL, nil, tenant, apiKey)
		if err == nil && getCode == http.StatusOK {
			var getResp SecretResponse
			if err := json.Unmarshal(respGet, &getResp); err == nil {
				envVar := fmt.Sprintf("%s=%s", getResp.Key, getResp.Value)
				secretEnvs = append(secretEnvs, envVar)
			}
		}
	}

	// 3. Spawns child process with env variables
	cmdParts := strings.Fields(command)
	if len(cmdParts) == 0 {
		log.Fatal("Command cannot be empty")
	}

	c := exec.Command(cmdParts[0], cmdParts[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	c.Env = append(os.Environ(), secretEnvs...)

	if err := c.Run(); err != nil {
		log.Fatalf("child command run failed: %v", err)
	}
}

func printUsage() {
	fmt.Println(`servsecretctl is a command-line client for ServSecret.

Usage:
  servsecretctl [command] [flags]

Commands:
  set           Create or update a secret
  get           Retrieve a secret
  delete        Delete a secret
  list          List all secret keys in tenant scope
  rotate        Rotate the master encryption key
  rollback      Rollback a secret to its previous version
  run           Run a command with secrets injected as environment variables
  health        Verify ServSecret service health

Flags:
  --url            ServSecret server URL (default: http://localhost:8091 or SERVSECRET_URL env)
  --api-key        API key for authentication (or SERVSECRET_API_KEY env)
  --tenant         Tenant ID for request isolation (default: default)
  --key            Secret key name (required for set/get/delete/rollback)
  --value          Secret value (required for set)
  --new-key        New 32-byte master key for rotation (required for rotate)
  --cmd            Command to run with injected secrets (required for run)`)
}
