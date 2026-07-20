package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vyuvaraj/serv/packages/ServShared"
	"gopkg.in/yaml.v3"
	"github.com/vyuvaraj/serv/packages/ServSecret/pkg/handlers"
	"github.com/vyuvaraj/serv/packages/ServSecret/pkg/storage"
)

type Config struct {
	Port            string   `yaml:"port"`
	FilePath        string   `yaml:"file_path"`
	MasterKey       string   `yaml:"master_key"`
	APIKeys         []string `yaml:"api_keys"`
	CacheTTLMinutes int      `yaml:"cache_ttl_minutes"`
}

func main() {
	// Client Mode Flags
	clientMode := flag.Bool("client", false, "Run in client mode to connect to a remote ServSecret instance")
	clientAction := flag.String("action", "", "Client action: set, get, delete, list, env")
	clientKey := flag.String("key", "", "Secret key for client action")
	clientValue := flag.String("value", "", "Secret value for client set action")
	clientURL := flag.String("server-url", "http://localhost:8091", "ServSecret server URL")
	clientTenant := flag.String("tenant-id", "default", "Tenant ID header")
	clientCmd := flag.String("cmd", "", "Command to execute with injected secrets (only used with --action=env)")
	clientAPIKey := flag.String("api-key", "", "API key for authentication header")

	configPath := flag.String("config", "", "Path to servsecret.yaml config file")
	portFlag := flag.String("port", "8091", "Port to listen on (override)")
	filePathFlag := flag.String("file", "secrets.enc", "Path to encrypted secrets file (override)")
	flag.Parse()

	if *clientMode {
		if *clientAction == "env" {
			runEnvInjector(*clientCmd, *clientURL, *clientTenant, *clientAPIKey)
			return
		}
		runClient(*clientAction, *clientKey, *clientValue, *clientURL, *clientTenant, *clientAPIKey)
		return
	}

	var cfg Config

	if *configPath != "" {
		cfgData, err := os.ReadFile(*configPath)
		if err != nil {
			log.Fatalf("failed to read config file: %v", err)
		}
		if err := yaml.Unmarshal(cfgData, &cfg); err != nil {
			log.Fatalf("failed to parse yaml config: %v", err)
		}
	} else {
		// Default config values
		cfg = Config{
			Port:            *portFlag,
			FilePath:        *filePathFlag,
			MasterKey:       os.Getenv("SERVSECRET_MASTER_KEY"),
			APIKeys:         []string{},
			CacheTTLMinutes: 5,
		}
		if envKeys := os.Getenv("SERVSECRET_API_KEYS"); envKeys != "" {
			cfg.APIKeys = strings.Split(envKeys, ",")
		}
	}

	log.Printf("Starting ServSecret Secret & Credential Manager on port %s...", cfg.Port)

	// Fetch master key
	var masterKey []byte
	var err error

	if cfg.MasterKey != "" {
		masterKey, err = hex.DecodeString(cfg.MasterKey)
		if err != nil || len(masterKey) != 32 {
			masterKey = []byte(cfg.MasterKey)
			if len(masterKey) != 32 {
				log.Println("WARNING: Master key is not 32 bytes. Adjusting key size...")
				padded := make([]byte, 32)
				copy(padded, masterKey)
				masterKey = padded
			}
		}
	} else {
		log.Println("WARNING: Master key not set. Generating temporary master key...")
		masterKey = make([]byte, 32)
		if _, err := rand.Read(masterKey); err != nil {
			log.Fatalf("failed to generate random temporary master key: %v", err)
		}
		log.Printf("Temporary master key (hex): %s", hex.EncodeToString(masterKey))
	}

	// Initialize Storage
	store, err := storage.NewEncryptedFileStore(cfg.FilePath, masterKey)
	if err != nil {
		log.Printf("Failed to initialize encrypted file store: %v. Falling back to in-memory store.", err)
		handlers.Store = storage.NewInMemoryStore()
	} else {
		handlers.Store = store
		// Start background tickers for backup and rotation
		startScheduledTasks(cfg.FilePath, store)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/readyz", ServShared.ReadyzHandler)
	mux.HandleFunc("/api/version", ServShared.VersionHandler("github.com/vyuvaraj/serv/packages/ServSecret", "1.0.0"))
	mux.HandleFunc("/api/v1/version", ServShared.VersionHandler("github.com/vyuvaraj/serv/packages/ServSecret", "1.0.0"))

	// Secret manager endpoints
	mux.HandleFunc("/api/secrets", handlers.HandleSecretRoute)
	mux.HandleFunc("/api/secrets/", handlers.HandleSecretRoute)
	mux.HandleFunc("/api/secrets/rollback", handlers.HandleSecretRollback)

	// Wrapper handler for /api/v1/ prefix rewriting
	v1Wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
		}
		mux.ServeHTTP(w, r)
	})

	var serverHandler http.Handler = handlers.LeakDetectionMiddleware(v1Wrapper)

	// If API Keys are configured, use API Key auth. Otherwise fallback to standard AuthMiddleware.
	if len(cfg.APIKeys) > 0 {
		log.Println("API Key protection enabled (zero-dependency standalone mode).")
		apiKeyAuth := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
					next.ServeHTTP(w, r)
					return
				}
				key := r.Header.Get("X-API-Key")
				if key == "" {
					authHeader := r.Header.Get("Authorization")
					if strings.HasPrefix(authHeader, "Bearer ") {
						key = strings.TrimPrefix(authHeader, "Bearer ")
					}
				}
				
				authorized := false
				for _, allowed := range cfg.APIKeys {
					if key == allowed {
						authorized = true
						break
					}
				}
				if !authorized {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte(`{"error": "Unauthorized: invalid or missing API key"}`))
					return
				}
				next.ServeHTTP(w, r)
			})
		}
		serverHandler = apiKeyAuth(v1Wrapper)
	} else {
		rateLimiter := ServShared.RateLimitMiddleware
		if flag.Lookup("test.v") != nil {
			rateLimiter = func(next http.Handler) http.Handler {
				return next
			}
		}
		serverHandler = ServShared.TraceMiddleware("github.com/vyuvaraj/serv/packages/ServSecret",
			rateLimiter(
				ServShared.CORSMiddleware(
					ServShared.MaxBytesMiddleware(10*1024*1024)(
						ServShared.AuthMiddleware(
							ServShared.TenantMiddleware(v1Wrapper),
						),
					),
				),
			),
		)
	}

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: serverHandler,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe failed: %v", err)
		}
	}()

	log.Printf("ServSecret is ready to manage credentials.")

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down ServSecret server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("ServSecret stopped cleanly.")
}

func runClient(action, key, val, serverURL, tenant, apiKey string) {
	if action == "" {
		log.Fatalf("Action is required (set, get, delete, list)")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	var req *http.Request
	var err error

	switch action {
	case "set":
		if key == "" || val == "" {
			log.Fatalf("Key and Value are required for set")
		}
		body, _ := json.Marshal(map[string]string{"key": key, "value": val})
		req, err = http.NewRequest(http.MethodPost, serverURL+"/api/v1/secrets", bytes.NewBuffer(body))
	case "get":
		if key == "" {
			log.Fatalf("Key is required for get")
		}
		req, err = http.NewRequest(http.MethodGet, serverURL+"/api/v1/secrets/"+key, nil)
	case "delete":
		if key == "" {
			log.Fatalf("Key is required for delete")
		}
		req, err = http.NewRequest(http.MethodDelete, serverURL+"/api/v1/secrets/"+key, nil)
	case "list":
		req, err = http.NewRequest(http.MethodGet, serverURL+"/api/v1/secrets", nil)
	default:
		log.Fatalf("Unknown action: %s", action)
	}

	if err != nil {
		log.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	fmt.Printf("Status: %d\n", resp.StatusCode)
	fmt.Printf("Response: %s\n", string(bodyBytes))
}

func runEnvInjector(command, serverURL, tenant, apiKey string) {
	if command == "" {
		log.Fatalf("Command is required (use --cmd)")
	}

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Fetch list of secrets keys
	reqList, err := http.NewRequest(http.MethodGet, serverURL+"/api/v1/secrets", nil)
	if err != nil {
		log.Fatalf("failed to create list request: %v", err)
	}
	reqList.Header.Set("X-Tenant-ID", tenant)
	if apiKey != "" {
		reqList.Header.Set("X-API-Key", apiKey)
	}

	respList, err := client.Do(reqList)
	if err != nil {
		log.Fatalf("failed to query secret list: %v", err)
	}
	defer respList.Body.Close()

	if respList.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(respList.Body)
		log.Fatalf("failed to retrieve secrets list: %d, response: %s", respList.StatusCode, string(body))
	}

	var listResp struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(respList.Body).Decode(&listResp); err != nil {
		log.Fatalf("failed to parse secret keys list: %v", err)
	}

	// 2. Query each secret and build env variables slice
	var secretEnvs []string
	for _, key := range listResp.Keys {
		reqGet, err := http.NewRequest(http.MethodGet, serverURL+"/api/v1/secrets/"+key, nil)
		if err != nil {
			log.Fatalf("failed to create get request: %v", err)
		}
		reqGet.Header.Set("X-Tenant-ID", tenant)
		if apiKey != "" {
			reqGet.Header.Set("X-API-Key", apiKey)
		}

		respGet, err := client.Do(reqGet)
		if err != nil {
			log.Fatalf("failed to fetch secret %s: %v", key, err)
		}
		defer respGet.Body.Close()

		if respGet.StatusCode == http.StatusOK {
			var getResp struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.NewDecoder(respGet.Body).Decode(&getResp); err == nil {
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

// Scheduled Tasks Helpers (SS.7, SS.9)
func startScheduledTasks(filePath string, store *storage.EncryptedFileStore) {
	// 1. Backup Ticker (every 24h)
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			PerformBackup(filePath)
		}
	}()

	// 2. Automated Key Rotation Ticker (every 90 days)
	go func() {
		ticker := time.NewTicker(90 * 24 * time.Hour)
		for range ticker.C {
			PerformKeyRotation(store)
		}
	}()
}

func PerformBackup(filePath string) error {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	backupDir := "backups"
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return err
	}

	backupPath := fmt.Sprintf("%s/secrets_%d.enc", backupDir, time.Now().UnixNano())
	return os.WriteFile(backupPath, data, 0600)
}

func PerformKeyRotation(store *storage.EncryptedFileStore) error {
	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		return err
	}
	return store.RotateMasterKey(newKey)
}
