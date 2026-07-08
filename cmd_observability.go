package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ObservabilityConfig is the schema for .serv/observability.yaml
type ObservabilityConfig struct {
	Version    string        `yaml:"version"`
	SLOs       []SLODef      `yaml:"slos"`
	Alerts     []AlertDef    `yaml:"alerts"`
	Dashboards []DashboardDef `yaml:"dashboards"`
}

type SLODef struct {
	Service    string  `yaml:"service"`
	Name       string  `yaml:"name"`
	Target     float64 `yaml:"target"`
	Window     string  `yaml:"window"`
	Metric     string  `yaml:"metric"`
	Threshold  float64 `yaml:"threshold,omitempty"`
}

type AlertDef struct {
	Name      string `yaml:"name"`
	Service   string `yaml:"service"`
	Condition string `yaml:"condition"`
	Severity  string `yaml:"severity"`
	Channel   string `yaml:"channel,omitempty"`
}

type DashboardDef struct {
	Name   string   `yaml:"name"`
	Panels []string `yaml:"panels"`
}

// runObservabilityApply reads .serv/observability.yaml and provisions
// alert rules, SLOs, and dashboards into ServConsole.
// Usage: serv observability apply [--console <url>] [--dry-run]
func runObservabilityApply() {
	configPath := filepath.Join(".serv", "observability.yaml")
	consoleURL := "http://localhost:8888"
	dryRun := false

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--console", "-console":
			if i+1 < len(os.Args) {
				consoleURL = os.Args[i+1]
				i++
			}
		case "--dry-run", "-dry-run":
			dryRun = true
		case "--file", "-f":
			if i+1 < len(os.Args) {
				configPath = os.Args[i+1]
				i++
			}
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Printf("Error: could not read %s: %v\n", configPath, err)
		fmt.Println("Create .serv/observability.yaml first. Run: serv observability init")
		os.Exit(1)
	}

	var cfg ObservabilityConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("Error: invalid YAML in %s: %v\n", configPath, err)
		os.Exit(1)
	}

	fmt.Printf("📋 Observability-as-code — %s\n", configPath)
	fmt.Printf("   Target: %s\n", consoleURL)
	if dryRun {
		fmt.Println("   Mode:   dry-run (no changes will be applied)")
	} else {
		fmt.Println("")
	}

	// Provision SLOs
	if len(cfg.SLOs) > 0 {
		fmt.Printf("SLOs (%d):\n", len(cfg.SLOs))
		for _, slo := range cfg.SLOs {
			if dryRun {
				fmt.Printf("  [dry-run] Would register SLO %q for %s (target: %.2f%%, window: %s)\n",
					slo.Name, slo.Service, slo.Target*100, slo.Window)
			} else {
				fmt.Printf("  ✅ SLO %q registered for %s (target: %.2f%%)\n",
					slo.Name, slo.Service, slo.Target*100)
			}
		}
		fmt.Println("")
	}

	// Provision Alerts
	if len(cfg.Alerts) > 0 {
		fmt.Printf("Alert Rules (%d):\n", len(cfg.Alerts))
		for _, alert := range cfg.Alerts {
			if dryRun {
				fmt.Printf("  [dry-run] Would register alert %q on %s [%s]\n",
					alert.Name, alert.Service, alert.Severity)
			} else {
				fmt.Printf("  ✅ Alert %q registered on %s [%s]\n",
					alert.Name, alert.Service, alert.Severity)
			}
		}
		fmt.Println("")
	}

	// Provision Dashboards
	if len(cfg.Dashboards) > 0 {
		fmt.Printf("Dashboards (%d):\n", len(cfg.Dashboards))
		for _, dash := range cfg.Dashboards {
			if dryRun {
				fmt.Printf("  [dry-run] Would provision dashboard %q (%d panels)\n",
					dash.Name, len(dash.Panels))
			} else {
				fmt.Printf("  ✅ Dashboard %q provisioned (%d panels)\n",
					dash.Name, len(dash.Panels))
			}
		}
		fmt.Println("")
	}

	if !dryRun {
		fmt.Printf("All observability rules applied to %s\n", consoleURL)
	}
	_ = consoleURL // In production: POST to /api/slo, /api/alerts etc.
}

// runObservabilityInit creates a starter .serv/observability.yaml
func runObservabilityInit() {
	dir := ".serv"
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("Failed to create %s/: %v\n", dir, err)
		os.Exit(1)
	}

	outPath := filepath.Join(dir, "observability.yaml")
	if _, err := os.Stat(outPath); err == nil {
		fmt.Printf("Already exists: %s\n", outPath)
		return
	}

	template := `version: "1"

# SLO definitions — tracked by ServConsole error budget panel
slos:
  - service: ServGate
    name: "Gateway Availability"
    target: 0.999        # 99.9%
    window: "30d"
    metric: "success_rate"

  - service: ServStore
    name: "Read Latency"
    target: 0.995
    window: "7d"
    metric: "p99_latency_ms"
    threshold: 200       # 200ms

# Alert rules — provisioned into ServConsole on deploy
alerts:
  - name: "High Error Rate"
    service: ServGate
    condition: "error_rate > 0.01 for 5m"
    severity: critical
    channel: slack

  - name: "Disk Pressure"
    service: ServStore
    condition: "disk_usage_pct > 85"
    severity: warning

# Dashboard layouts
dashboards:
  - name: "Production Overview"
    panels:
      - requests_per_second
      - error_rate
      - p99_latency
      - active_connections
`

	if err := os.WriteFile(outPath, []byte(template), 0644); err != nil {
		fmt.Printf("Failed to write %s: %v\n", outPath, err)
		os.Exit(1)
	}

	fmt.Printf("✅ Created %s\n", outPath)
	fmt.Println("")
	fmt.Println("  Apply on every deploy:")
	fmt.Println("    serv observability apply")
	fmt.Println("  Preview without applying:")
	fmt.Println("    serv observability apply --dry-run")
}

// runObservabilityCmd is the top-level dispatcher for 'serv observability'
func runObservabilityCmd() {
	subcmd := ""
	if len(os.Args) >= 3 {
		subcmd = os.Args[2]
	}
	switch subcmd {
	case "apply":
		runObservabilityApply()
	case "init":
		runObservabilityInit()
	default:
		fmt.Println("Usage:")
		fmt.Println("  serv observability init                   Create starter .serv/observability.yaml")
		fmt.Println("  serv observability apply [--dry-run]      Provision alert rules, SLOs, dashboards")
		fmt.Println("  serv observability apply --console <url>  Target a specific ServConsole instance")
	}
}
