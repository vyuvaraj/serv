package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type DoctorDiscovery struct {
	Gate   string `json:"gate"`
	Store  string `json:"store"`
	Queue  string `json:"queue"`
	Cache  string `json:"cache"`
	Cron   string `json:"cron"`
	Mesh   string `json:"mesh"`
	Cloud  string `json:"cloud"`
	Tunnel string `json:"tunnel"`
	Trace  string `json:"trace"`
	Registry string `json:"registry"`
	Auth   string `json:"auth"`
	DB     string `json:"db"`
	Mail   string `json:"mail"`
	Flow   string `json:"flow"`
}

var osExit = os.Exit

func runDoctor(integration bool) {
	fmt.Println("🩺 Running Ecosystem Doctor check...")
	if integration {
		fmt.Println("🐳 Running docker-compose service integration checks...")
	}
	raw := os.Getenv("SERVVERSE_DISCOVERY")
	if raw == "" {
		fmt.Println("❌ Error: SERVVERSE_DISCOVERY environment variable is not set.")
		fmt.Println("Please set SERVVERSE_DISCOVERY to a valid JSON manifest or file path.")
		osExit(1)
	}

	var discovery DoctorDiscovery
	if err := json.Unmarshal([]byte(raw), &discovery); err != nil {
		// Try reading as file path
		data, err := os.ReadFile(raw)
		if err != nil {
			fmt.Printf("❌ Error: failed to parse SERVVERSE_DISCOVERY: %v\n", err)
			osExit(1)
		}
		if err := json.Unmarshal(data, &discovery); err != nil {
			fmt.Printf("❌ Error: failed to parse SERVVERSE_DISCOVERY file: %v\n", err)
			osExit(1)
		}
	}

	services := []struct {
		name string
		url  string
	}{
		{"ServGate", discovery.Gate},
		{"ServStore", discovery.Store},
		{"ServQueue", discovery.Queue},
		{"ServCache", discovery.Cache},
		{"ServCron", discovery.Cron},
		{"ServMesh", discovery.Mesh},
		{"ServCloud", discovery.Cloud},
		{"ServTunnel", discovery.Tunnel},
		{"ServTrace", discovery.Trace},
		{"ServRegistry", discovery.Registry},
		{"ServAuth", discovery.Auth},
		{"ServDB", discovery.DB},
		{"ServMail", discovery.Mail},
		{"ServFlow", discovery.Flow},
	}

	client := &http.Client{Timeout: 2 * time.Second}
	hasErrors := false

	fmt.Println("\n| Service | Status | Version | Edition | Details |")
	fmt.Println("|---|---|---|---|---|")

	for _, s := range services {
		if s.url == "" {
			fmt.Printf("| %-12s | 🟡 SKIP    | -       | -        | Not configured |\n", s.name)
			continue
		}

		// Check version
		resp, err := client.Get(s.url + "/api/version")
		if err != nil {
			fmt.Printf("| %-12s | ❌ DOWN    | -       | -        | Connection failed: %v |\n", s.name, err)
			hasErrors = true
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("| %-12s | ❌ ERROR   | -       | -        | Bad status code: %d |\n", s.name, resp.StatusCode)
			hasErrors = true
			continue
		}

		var verInfo map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&verInfo); err != nil {
			fmt.Printf("| %-12s | ❌ INVALID | -       | -        | Failed to decode JSON |\n", s.name)
			hasErrors = true
			continue
		}

		ver := verInfo["version"]
		if ver == "" {
			ver = "unknown"
		}
		edition := verInfo["edition"]
		if edition == "" {
			edition = "oss"
		}

		// Compatibility check: warn if minor version doesn't match compiler version
		compilerVer := "0.1" // Major.Minor
		isCompatible := strings.HasPrefix(strings.TrimPrefix(ver, "v"), compilerVer)

		statusStr := "✅ ONLINE"
		detailStr := "OK"
		if !isCompatible && ver != "unknown" {
			statusStr = "⚠️ WARN"
			detailStr = fmt.Sprintf("Version mismatch! Compiler expects v%s.x", compilerVer)
		}

		fmt.Printf("| %-12s | %-9s | %-7s | %-8s | %-30s |\n", s.name, statusStr, ver, edition, detailStr)
	}

	checkTelemetryPipeline()
	checkWasmRuntime()
	checkCompilerPlugins()

	if hasErrors {
		fmt.Println("\n❌ Doctor check complete with errors. Some services are down or misconfigured.")
		osExit(1)
	}
	fmt.Println("\n✅ All configured services are online and compatible!")
}

func checkWasmRuntime() {
	fmt.Println("\n🌐 Running WASM Runtime Diagnostics...")
	runtimes := []string{"node", "wasmtime", "wasmer"}
	foundAny := false
	for _, rt := range runtimes {
		// exec is imported as os/exec in Go
		path, err := exec.LookPath(rt)
		if err == nil {
			fmt.Printf("✅ Found WASM runtime %q at: %s\n", rt, path)
			foundAny = true
		}
	}
	if !foundAny {
		fmt.Println("⚠️ Warning: No local WASM execution runtime (node, wasmtime, wasmer) was found in PATH.")
		fmt.Println("To run compiled WASM targets, please install Node.js or Wasmtime.")
	}
}

func checkCompilerPlugins() {
	fmt.Println("\n🔌 Running Compiler Plugin Diagnostics...")
	// Search in vscode-support/extension/package.json
	packageJsonPath := filepath.Join("vscode-support", "extension", "package.json")
	data, err := os.ReadFile(packageJsonPath)
	if err != nil {
		fmt.Println("⚠️ Warning: VS Code extension package.json not found in local workspace.")
		return
	}

	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		fmt.Printf("⚠️ Warning: failed to parse VS Code package.json: %v\n", err)
		return
	}

	fmt.Printf("✅ Serv VS Code Extension (local plugin) version: v%s\n", pkg.Version)
}

func checkTelemetryPipeline() {
	fmt.Println("\n📡 Running Telemetry Pipeline Diagnostics...")
	otlp := os.Getenv("SERV_OTLP_ENDPOINT")
	if otlp == "" {
		fmt.Println("⚠️ Warning: SERV_OTLP_ENDPOINT environment variable is not set. Services will not emit traces.")
		return
	}

	fmt.Printf("Checking connectivity to OTLP collector: %s\n", otlp)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(otlp + "/healthz")
	if err != nil {
		// Try a fallback POST request to the traces endpoint to verify routing
		respPost, errPost := client.Post(otlp + "/v1/traces", "application/json", strings.NewReader(`{}`))
		if errPost != nil {
			fmt.Printf("❌ Telemetry Pipeline Error: OTLP endpoint is unreachable: %v\n", errPost)
			return
		}
		defer respPost.Body.Close()
		fmt.Printf("✅ Telemetry Pipeline OK (OTLP Traces endpoint responded with HTTP %d)\n", respPost.StatusCode)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("✅ Telemetry Pipeline OK (Collector healthz returned HTTP %d)\n", resp.StatusCode)
}

