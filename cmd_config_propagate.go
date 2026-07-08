package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ServConfig is the schema for .serv/config.yaml
type ServConfig struct {
	Version  string                 `yaml:"version"`
	Services map[string]ServiceCfg  `yaml:"services"`
	Global   map[string]interface{} `yaml:"global"`
}

type ServiceCfg struct {
	URL     string                 `yaml:"url"`
	Options map[string]interface{} `yaml:"options"`
}

// runConfigPropagate reads .serv/config.yaml and pushes config to all
// running Servverse services via their /api/config endpoint.
// Usage: serv config propagate [--file <path>] [--dry-run] [--services <list>]
func runConfigPropagate() {
	configPath := filepath.Join(".serv", "config.yaml")
	dryRun := false
	servicesFilter := ""

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--file", "-f":
			if i+1 < len(os.Args) {
				configPath = os.Args[i+1]
				i++
			}
		case "--dry-run", "-dry-run":
			dryRun = true
		case "--services", "-services", "-s":
			if i+1 < len(os.Args) {
				servicesFilter = os.Args[i+1]
				i++
			}
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Printf("Error: could not read %s: %v\n", configPath, err)
		fmt.Println("Run 'serv config init' to create a starter .serv/config.yaml")
		os.Exit(1)
	}

	var cfg ServConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("Error: invalid YAML in %s: %v\n", configPath, err)
		os.Exit(1)
	}

	filter := map[string]bool{}
	if servicesFilter != "" {
		for _, s := range strings.Split(servicesFilter, ",") {
			filter[strings.TrimSpace(strings.ToLower(s))] = true
		}
	}

	fmt.Printf("⚙  Config Propagation — %s\n", configPath)
	if dryRun {
		fmt.Println("   Mode: dry-run\n")
	} else {
		fmt.Println("")
	}

	// Push global config to each service
	client := &http.Client{Timeout: 5 * time.Second}
	pushed := 0
	failed := 0

	for svcName, svcCfg := range cfg.Services {
		if len(filter) > 0 && !filter[strings.ToLower(svcName)] {
			continue
		}

		// Merge global + service-specific options
		merged := map[string]interface{}{}
		for k, v := range cfg.Global {
			merged[k] = v
		}
		for k, v := range svcCfg.Options {
			merged[k] = v
		}

		if dryRun {
			fmt.Printf("  [dry-run] Would push %d keys to %s (%s)\n",
				len(merged), svcName, svcCfg.URL)
			pushed++
			continue
		}

		url := strings.TrimSuffix(svcCfg.URL, "/") + "/api/config"
		body, _ := json.Marshal(merged)
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			fmt.Printf("  ⚠️  %s (%s): unreachable — %v\n", svcName, svcCfg.URL, err)
			failed++
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			fmt.Printf("  ✅ %s — %d keys pushed\n", svcName, len(merged))
			pushed++
		} else {
			fmt.Printf("  ⚠️  %s — HTTP %d\n", svcName, resp.StatusCode)
			failed++
		}
	}

	fmt.Printf("\nPropagated to %d service(s)", pushed)
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println("")
}

// runConfigInit creates a starter .serv/config.yaml
func runConfigInit() {
	dir := ".serv"
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("Failed to create %s/: %v\n", dir, err)
		os.Exit(1)
	}

	outPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(outPath); err == nil {
		fmt.Printf("Already exists: %s\n", outPath)
		return
	}

	template := `version: "1"

# Global config applied to all services
global:
  log_level: info
  otel_endpoint: http://localhost:4317
  environment: development

# Per-service overrides and connection URLs
services:
  ServGate:
    url: http://localhost:8080
    options:
      rate_limit_rps: 1000
      timeout_ms: 5000

  ServCache:
    url: http://localhost:8086
    options:
      default_ttl_secs: 300
      max_entries: 10000

  ServCron:
    url: http://localhost:8087
    options:
      max_concurrent_jobs: 10

  ServStore:
    url: http://localhost:9000
    options:
      replication_factor: 2

  ServQueue:
    url: http://localhost:8085
    options:
      retention_hours: 24
`

	if err := os.WriteFile(outPath, []byte(template), 0644); err != nil {
		fmt.Printf("Failed to write %s: %v\n", outPath, err)
		os.Exit(1)
	}

	fmt.Printf("✅ Created %s\n", outPath)
	fmt.Println("")
	fmt.Println("  Propagate config on every deploy:")
	fmt.Println("    serv config propagate")
	fmt.Println("  Preview without applying:")
	fmt.Println("    serv config propagate --dry-run")
}

// runConfigCmd is the top-level dispatcher for 'serv config'
func runConfigCmd() {
	subcmd := ""
	if len(os.Args) >= 3 {
		subcmd = os.Args[2]
	}
	switch subcmd {
	case "propagate", "push":
		runConfigPropagate()
	case "init":
		runConfigInit()
	default:
		fmt.Println("Usage:")
		fmt.Println("  serv config init                              Create starter .serv/config.yaml")
		fmt.Println("  serv config propagate [--dry-run]            Push config to all running services")
		fmt.Println("  serv config propagate --services gate,cache  Push to specific services only")
	}
}
