package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// servDevServices defines the default services to start in dev mode.
var servDevServices = []struct {
	Name    string
	Port    string
	Binary  string
	Args    []string
	EnvVars []string
}{
	{"ServStore", "8081", "servstore", []string{"--port", "8081", "--data-dir", ".servdev/store-data"}, nil},
	{"ServQueue", "8082", "servqueue", nil, nil},
	{"ServCache", "8086", "servcache", nil, nil},
	{"ServCron", "8087", "servcron", nil, []string{"PORT=8087"}},
	{"ServGate", "8080", "servgate", nil, nil},
	{"ServTrace", "8090", "servtrace", nil, nil},
}

func runDevCmd() {
	devCmd := flag.NewFlagSet("dev", flag.ExitOnError)
	servicesFlag := devCmd.String("services", "store,queue,cache,gate", "Comma-separated services to start (store,queue,cache,cron,gate,trace,all)")
	portFlag := devCmd.String("port", "8080", "Port for the user's .srv service")
	noConsoleFlag := devCmd.Bool("no-console", false, "Skip opening ServConsole dashboard")
	dashboardFlag := devCmd.Bool("dashboard", false, "Show live terminal TUI dashboard with service health and stats")
	if err := devCmd.Parse(os.Args[2:]); err != nil {
		fmt.Printf("Error parsing arguments: %v\n", err)
		os.Exit(1)
	}

	args := devCmd.Args()
	srvFile := "."
	if len(args) > 0 {
		srvFile = args[0]
	}

	fmt.Println()
	fmt.Println("  ▲ Serv Dev Environment")
	fmt.Println("  ━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// Create dev data directory
	os.MkdirAll(".servdev/store-data", 0755)

	// Determine which services to start
	requestedServices := parseServiceList(*servicesFlag)

	// Start infrastructure services
	var procs []*devProcess
	var wg sync.WaitGroup

	for _, svc := range servDevServices {
		if !requestedServices[svc.Name] {
			continue
		}

		// Check if the binary is available on PATH
		_, err := exec.LookPath(svc.Binary)
		if err != nil {
			fmt.Printf("  ⚠  %s binary not found (%s). Skipping.\n", svc.Name, svc.Binary)
			continue
		}

		proc := startDevService(svc.Name, svc.Binary, svc.Port, svc.Args, svc.EnvVars)
		if proc != nil {
			procs = append(procs, proc)
			fmt.Printf("  ✓  %s started on :%s\n", svc.Name, svc.Port)
		}
	}

	fmt.Println()

	// Wait for services to be healthy
	fmt.Print("  Waiting for services...")
	time.Sleep(2 * time.Second)
	healthyCount := 0
	for _, proc := range procs {
		if checkDevHealth(proc.port) {
			healthyCount++
		}
	}
	fmt.Printf(" %d/%d healthy\n", healthyCount, len(procs))
	fmt.Println()

	// Start the user's .srv file with hot reload
	fmt.Printf("  ▶  Starting %s with hot-reload on :%s\n", srvFile, *portFlag)
	fmt.Println()

	// Set environment for the user service to discover infra
	os.Setenv("SERV_STORE_ENDPOINT", "http://localhost:8081")
	os.Setenv("SERV_QUEUE_ENDPOINT", "localhost:8082")
	os.Setenv("SERV_CACHE_ENDPOINT", "http://localhost:8086")
	os.Setenv("SERV_GATE_ENDPOINT", "http://localhost:8080")
	os.Setenv("SERV_TRACE_ENDPOINT", "http://localhost:8090")

	if !*noConsoleFlag {
		fmt.Println("  Dashboard: http://localhost:8083 (start ServConsole separately)")
	}

	fmt.Println("  ━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Press Ctrl+C to stop all services\n\n")

	// Run the user service with hot reload
	if *dashboardFlag {
		go startDevDashboard(procs)
	}
	startWorkspaceWatcher(procs)
	wg.Add(1)
	go func() {
		defer wg.Done()
		runServHot(srvFile, "")
	}()

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n  Shutting down dev environment...")
	for _, proc := range procs {
		if proc.cmd != nil && proc.cmd.Process != nil {
			proc.cmd.Process.Kill()
		}
	}
	fmt.Println("  ✓ All services stopped.")
}

type devProcess struct {
	name    string
	port    string
	binary  string
	args    []string
	envVars []string
	cmd     *exec.Cmd
}

func startDevService(name, binary, port string, args, envVars []string) *devProcess {
	cmd := exec.Command(binary, args...)
	cmd.Stdout = nil // Suppress output in dev mode
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(), envVars...)

	if err := cmd.Start(); err != nil {
		log.Printf("  ✗  Failed to start %s: %v", name, err)
		return nil
	}

	return &devProcess{
		name:    name,
		port:    port,
		binary:  binary,
		args:    args,
		envVars: envVars,
		cmd:     cmd,
	}
}

func checkDevHealth(port string) bool {
	client := http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%s/healthz", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func parseServiceList(input string) map[string]bool {
	result := make(map[string]bool)
	if input == "all" {
		for _, svc := range servDevServices {
			result[svc.Name] = true
		}
		return result
	}
	for _, s := range splitComma(input) {
		switch s {
		case "store":
			result["ServStore"] = true
		case "queue":
			result["ServQueue"] = true
		case "cache":
			result["ServCache"] = true
		case "cron":
			result["ServCron"] = true
		case "gate":
			result["ServGate"] = true
		case "trace":
			result["ServTrace"] = true
		}
	}
	return result
}

func splitComma(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == ',' {
			if current != "" {
				result = append(result, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func startWorkspaceWatcher(procs []*devProcess) {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	parentDir := filepath.Dir(wd)

	dirMap := make(map[string]string)
	for _, proc := range procs {
		dirMap[proc.name] = filepath.Join(parentDir, proc.name)
	}

	getModTimes := func(dir string) map[string]time.Time {
		times := make(map[string]time.Time)
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && filepath.Ext(path) == ".go" {
				times[path] = info.ModTime()
			}
			return nil
		})
		return times
	}

	lastMods := make(map[string]map[string]time.Time)
	for name, dir := range dirMap {
		lastMods[name] = getModTimes(dir)
	}

	go func() {
		for {
			time.Sleep(1 * time.Second)
			for _, proc := range procs {
				dir, exists := dirMap[proc.name]
				if !exists {
					continue
				}
				current := getModTimes(dir)
				changed := false
				for path, mtime := range current {
					oldTime, ok := lastMods[proc.name][path]
					if !ok || mtime.After(oldTime) {
						changed = true
						break
					}
				}
				if !changed && len(current) != len(lastMods[proc.name]) {
					changed = true
				}

				if changed {
					lastMods[proc.name] = current
					log.Printf("[WORKSPACE HOT-RELOAD] Change detected in %s. Rebuilding service...", proc.name)

					buildCmd := exec.Command("go", "build", "-o", proc.binary+".exe")
					buildCmd.Dir = dir
					buildCmd.Env = append(os.Environ(), "GOWORK=off")
					if err := buildCmd.Run(); err != nil {
						log.Printf("[WORKSPACE HOT-RELOAD] Build failed for %s: %v", proc.name, err)
						continue
					}

					if proc.cmd != nil && proc.cmd.Process != nil {
						proc.cmd.Process.Kill()
						proc.cmd.Wait()
					}

					binaryPath := filepath.Join(dir, proc.binary+".exe")
					newCmd := exec.Command(binaryPath, proc.args...)
					newCmd.Stdout = nil
					newCmd.Stderr = nil
					newCmd.Env = append(os.Environ(), proc.envVars...)
					if err := newCmd.Start(); err != nil {
						log.Printf("[WORKSPACE HOT-RELOAD] Restart failed for %s: %v", proc.name, err)
					} else {
						proc.cmd = newCmd
						log.Printf("[WORKSPACE HOT-RELOAD] Successfully restarted %s", proc.name)
					}
				}
			}
		}
	}()
}

// startDevDashboard runs a periodic terminal refresh showing live service health,
// implementing the DX.26 k9s-style terminal dashboard for 'serv dev --dashboard'.
func startDevDashboard(procs []*devProcess) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	clearLine := "\033[2J\033[H" // ANSI clear screen + move to top

	for range ticker.C {
		timestamp := time.Now().Format("15:04:05")

		// Build status board
		var sb strings.Builder
		sb.WriteString(clearLine)
		sb.WriteString("┌─────────────────────────────────────────────────────────────┐\n")
		sb.WriteString(fmt.Sprintf("│  ▲ Serv Dev Dashboard                         %s  │\n", timestamp))
		sb.WriteString("├──────────────────┬──────────┬──────────┬────────────────────┤\n")
		sb.WriteString("│ SERVICE          │ PORT     │ STATUS   │ HEALTH             │\n")
		sb.WriteString("├──────────────────┼──────────┼──────────┼────────────────────┤\n")

		for _, proc := range procs {
			running := proc.cmd != nil && proc.cmd.Process != nil
			healthy := false
			if running {
				healthy = checkDevHealth(proc.port)
			}

			status := "  ●  DOWN"
			if running && healthy {
				status = "  ●  UP  "
			} else if running {
				status = "  ●  STARTING"
			}

			healthStr := "─"
			if healthy {
				healthStr = "OK"
			}

			sb.WriteString(fmt.Sprintf("│ %-16s │ %-8s │ %-8s │ %-18s │\n",
				proc.name, ":"+proc.port, status, healthStr))
		}

		sb.WriteString("└──────────────────┴──────────┴──────────┴────────────────────┘\n")
		sb.WriteString("  Press Ctrl+C to stop all services\n")

		fmt.Print(sb.String())
	}
}

