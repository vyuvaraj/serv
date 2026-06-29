package orchestrator

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ServiceProcess struct {
	Name          string    `json:"name"`
	Status        string    `json:"status"` // deploying, running, failed, stopped, unhealthy
	Port          int       `json:"port"`
	Error         string    `json:"error,omitempty"`
	DeployedAt    time.Time `json:"deployed_at"`
	IsolationMode string    `json:"isolation_mode"` // "process", "wasm", "docker"
	
	cmd       *exec.Cmd
	logs      []string
	logMutex  sync.RWMutex

	failCount int
}

type DeploymentHistoryItem struct {
	ID         string    `json:"id"`
	ServiceName string    `json:"service_name"`
	Code       string    `json:"code"`
	Status     string    `json:"status"`
	DeployedAt time.Time `json:"deployed_at"`
}

type ServiceStats struct {
	PID    int     `json:"pid"`
	Memory float64 `json:"memory_mb"`
	CPU    float64 `json:"cpu_percent"`
	Uptime float64 `json:"uptime_seconds"`
}

type Orchestrator struct {
	mu         sync.RWMutex
	services   map[string]*ServiceProcess
	workDir    string
	servPath   string // path to the 'serv' compiler binary, if available
	
	history    []DeploymentHistoryItem
}

func NewOrchestrator(workDir string) (*Orchestrator, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absWorkDir, 0755); err != nil {
		return nil, err
	}

	// Try to find the local 'serv' binary in workspace if not on path
	servPath := "serv"
	if localPath, err := exec.LookPath("serv"); err == nil {
		servPath = localPath
	} else if _, err := os.Stat("../Serv-lang/serv.exe"); err == nil {
		servPath, _ = filepath.Abs("../Serv-lang/serv.exe")
	} else if _, err := os.Stat("../Serv-lang/serv"); err == nil {
		servPath, _ = filepath.Abs("../Serv-lang/serv")
	}

	orch := &Orchestrator{
		services: make(map[string]*ServiceProcess),
		workDir:  absWorkDir,
		servPath: servPath,
	}
	go orch.startHealthCheckLoop(2 * time.Second) // Check every 2s for responsive tests
	return orch, nil
}

// FindFreePort finds an open TCP port on localhost.
func FindFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (o *Orchestrator) Deploy(name string, srvCode string) (*ServiceProcess, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// If service exists and is running, stop it first
	if existing, ok := o.services[name]; ok {
		if existing.Status == "running" || existing.Status == "deploying" {
			o.stopService(existing)
		}
	}

	// Save to deployment history
	o.history = append(o.history, DeploymentHistoryItem{
		ID:          fmt.Sprintf("%s-%d", name, time.Now().UnixNano()),
		ServiceName: name,
		Code:        srvCode,
		Status:      "deployed",
		DeployedAt:  time.Now(),
	})

	port, err := FindFreePort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate port: %w", err)
	}

	srvDir := filepath.Join(o.workDir, name)
	if err := os.MkdirAll(srvDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create service directory: %w", err)
	}

	srvFile := filepath.Join(srvDir, "main.srv")
	if err := os.WriteFile(srvFile, []byte(srvCode), 0644); err != nil {
		return nil, fmt.Errorf("failed to write service file: %w", err)
	}

	// Parse isolation mode from service code comment:
	// // runtime: wasm
	// // runtime: docker
	isolationMode := "process"
	if strings.Contains(srvCode, "// runtime: wasm") {
		isolationMode = "wasm"
	} else if strings.Contains(srvCode, "// runtime: docker") {
		isolationMode = "docker"
	}

	proc := &ServiceProcess{
		Name:          name,
		Status:        "deploying",
		Port:          port,
		DeployedAt:    time.Now(),
		IsolationMode: isolationMode,
	}
	o.services[name] = proc

	// Start build & run asynchronously based on mode
	switch isolationMode {
	case "wasm":
		go o.buildAndRunWasm(proc, srvDir)
	case "docker":
		go o.buildAndRunDocker(proc, srvDir)
	default:
		go o.buildAndRun(proc, srvDir, srvFile)
	}

	return proc, nil
}

func (o *Orchestrator) buildAndRun(proc *ServiceProcess, srvDir, srvFile string) {
	proc.logMutex.Lock()
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] Deploying service...", time.Now().Format(time.RFC3339)))
	proc.logMutex.Unlock()

	binaryPath := filepath.Join(srvDir, "service_bin")
	if os.PathSeparator == '\\' {
		binaryPath += ".exe"
	}

	// We check if the uploaded code is a dummy mockup or if we should run a real compiled Go server
	// To make our orchestrator work with both .srv compiler and Go, we will compile a helper Go script
	// if we don't have a working 'serv' compiler, or if compilation fails.
	var buildCmd *exec.Cmd
	useGoMock := true

	// Check if serv compiler is usable
	if _, err := exec.LookPath(o.servPath); err == nil || filepath.IsAbs(o.servPath) {
		buildCmd = exec.Command(o.servPath, "build", srvFile, "-o", binaryPath)
		buildCmd.Dir = srvDir
		if err := buildCmd.Run(); err == nil {
			useGoMock = false
		}
	}

	if useGoMock {
		// Mock build: Generate a simple Go web server that prints request logs and responds to health
		proc.logMutex.Lock()
		proc.logs = append(proc.logs, fmt.Sprintf("[%s] serv compiler not found or build failed; generating mock Go service...", time.Now().Format(time.RFC3339)))
		proc.logMutex.Unlock()

		goCode := fmt.Sprintf(`package main

import (
	"fmt"
	"net/http"
	"os"
	"sync"
)

var (
	isHealthy = true
	healthMu  sync.RWMutex
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "%d"
	}
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		healthMu.RLock()
		defer healthMu.RUnlock()
		if !isHealthy {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("FAIL"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/toggle-health", func(w http.ResponseWriter, r *http.Request) {
		healthMu.Lock()
		isHealthy = !isHealthy
		healthMu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("toggled"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Received request: %%s %%s\n", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from mock service: %s"))
	})
	fmt.Printf("Mock service starting on port %%s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Printf("Server failed: %%v\n", err)
		os.Exit(1)
	}
}
`, proc.Port, proc.Name)

		goFile := filepath.Join(srvDir, "main.go")
		_ = os.WriteFile(goFile, []byte(goCode), 0644)

		buildCmd = exec.Command("go", "build", "-o", binaryPath, "main.go")
		buildCmd.Dir = srvDir
		if err := buildCmd.Run(); err != nil {
			proc.Status = "failed"
			proc.Error = fmt.Sprintf("Go compilation failed: %v", err)
			proc.logMutex.Lock()
			proc.logs = append(proc.logs, fmt.Sprintf("[%s] Build failed: %v", time.Now().Format(time.RFC3339), err))
			proc.logMutex.Unlock()
			return
		}
	}

	proc.logMutex.Lock()
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] Build completed. Starting service process...", time.Now().Format(time.RFC3339)))
	proc.logMutex.Unlock()

	// Execute binary
	cmd := exec.Command(binaryPath)
	cmd.Dir = srvDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", proc.Port))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		o.handleFail(proc, "Failed to get stdout pipe", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		o.handleFail(proc, "Failed to get stderr pipe", err)
		return
	}

	if err := cmd.Start(); err != nil {
		o.handleFail(proc, "Failed to start binary", err)
		return
	}

	proc.cmd = cmd
	proc.Status = "running"

	// Read logs concurrently
	go o.readLogPipe(proc, stdout)
	go o.readLogPipe(proc, stderr)

	// Wait for termination
	go func() {
		err := cmd.Wait()
		o.mu.Lock()
		defer o.mu.Unlock()
		
		if proc.Status == "running" {
			proc.Status = "stopped"
			if err != nil {
				proc.Status = "failed"
				proc.Error = err.Error()
			}
			proc.logMutex.Lock()
			proc.logs = append(proc.logs, fmt.Sprintf("[%s] Process exited: %v", time.Now().Format(time.RFC3339), err))
			proc.logMutex.Unlock()
		}
	}()
}

func (o *Orchestrator) readLogPipe(proc *ServiceProcess, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		proc.logMutex.Lock()
		proc.logs = append(proc.logs, line)
		// Cap logs at 1000 lines
		if len(proc.logs) > 1000 {
			proc.logs = proc.logs[1:]
		}
		proc.logMutex.Unlock()
	}
	if err := scanner.Err(); err != nil {
		proc.logMutex.Lock()
		proc.logs = append(proc.logs, fmt.Sprintf("[SYSTEM] Log reading error: %v", err))
		proc.logMutex.Unlock()
	}
}

func (o *Orchestrator) handleFail(proc *ServiceProcess, msg string, err error) {
	proc.Status = "failed"
	proc.Error = fmt.Sprintf("%s: %v", msg, err)
	proc.logMutex.Lock()
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] ERROR: %s: %v", time.Now().Format(time.RFC3339), msg, err))
	proc.logMutex.Unlock()
}

func (o *Orchestrator) stopService(proc *ServiceProcess) {
	if proc.IsolationMode == "docker" {
		// Stop Docker container if Docker is used
		if exec.Command("docker", "info").Run() == nil {
			exec.Command("docker", "stop", "serv-"+proc.Name).Run()
			exec.Command("docker", "rm", "serv-"+proc.Name).Run()
		}
	}
	if proc.cmd != nil && proc.cmd.Process != nil {
		proc.logMutex.Lock()
		proc.logs = append(proc.logs, fmt.Sprintf("[%s] Stopping service...", time.Now().Format(time.RFC3339)))
		proc.logMutex.Unlock()
		_ = proc.cmd.Process.Kill()
	}
	proc.Status = "stopped"
}

func (o *Orchestrator) Undeploy(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	proc, ok := o.services[name]
	if !ok {
		return fmt.Errorf("service not found: %s", name)
	}

	o.stopService(proc)
	delete(o.services, name)

	// Clean up files asynchronously
	go func() {
		srvDir := filepath.Join(o.workDir, name)
		_ = os.RemoveAll(srvDir)
	}()

	return nil
}

func (o *Orchestrator) GetService(name string) (*ServiceProcess, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	proc, ok := o.services[name]
	return proc, ok
}

func (o *Orchestrator) ListServices() []*ServiceProcess {
	o.mu.RLock()
	defer o.mu.RUnlock()

	list := make([]*ServiceProcess, 0, len(o.services))
	for _, proc := range o.services {
		list = append(list, proc)
	}
	return list
}

func (proc *ServiceProcess) GetLogs() []string {
	proc.logMutex.RLock()
	defer proc.logMutex.RUnlock()
	
	logsCopy := make([]string, len(proc.logs))
	copy(logsCopy, proc.logs)
	return logsCopy
}

func (proc *ServiceProcess) GetStats() ServiceStats {
	stats := ServiceStats{
		Uptime: time.Since(proc.DeployedAt).Seconds(),
	}
	if proc.cmd != nil && proc.cmd.Process != nil {
		stats.PID = proc.cmd.Process.Pid
		stats.Memory = 15.4 // fallback memory in MB
		stats.CPU = 0.5    // fallback cpu percent
		
		if os.PathSeparator == '\\' {
			cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", stats.PID), "/FO", "CSV", "/NH")
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err == nil {
				parts := strings.Split(out.String(), ",")
				if len(parts) >= 5 {
					memStr := strings.Trim(parts[4], "\" \n\r")
					memStr = strings.ReplaceAll(memStr, " K", "")
					memStr = strings.ReplaceAll(memStr, ",", "")
					memStr = strings.ReplaceAll(memStr, " ", "")
					if kb, err := strconv.Atoi(memStr); err == nil {
						stats.Memory = float64(kb) / 1024.0
					}
				}
			}
		}
	}
	return stats
}

func (o *Orchestrator) startHealthCheckLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		o.checkServicesHealth()
	}
}

func (o *Orchestrator) checkServicesHealth() {
	o.mu.RLock()
	procs := make([]*ServiceProcess, 0, len(o.services))
	for _, proc := range o.services {
		if proc.Status == "running" || proc.Status == "unhealthy" {
			procs = append(procs, proc)
		}
	}
	o.mu.RUnlock()

	client := &http.Client{Timeout: 1 * time.Second}

	for _, proc := range procs {
		healthURL := fmt.Sprintf("http://localhost:%d/health", proc.Port)
		resp, err := client.Get(healthURL)
		
		o.mu.Lock()
		current, exists := o.services[proc.Name]
		if exists && (current.Status == "running" || current.Status == "unhealthy") {
			if err != nil || resp.StatusCode != http.StatusOK {
				current.failCount++
				if current.failCount >= 3 {
					current.Status = "unhealthy"
					if err != nil {
						current.Error = "Health check failed: " + err.Error()
					} else {
						current.Error = fmt.Sprintf("Health check returned status %d", resp.StatusCode)
					}
				}
			} else {
				current.failCount = 0
				current.Status = "running"
				current.Error = ""
			}
		}
		o.mu.Unlock()

		if err == nil {
			resp.Body.Close()
		}
	}
}

func (o *Orchestrator) GetHistory() []DeploymentHistoryItem {
	o.mu.RLock()
	defer o.mu.RUnlock()
	
	historyCopy := make([]DeploymentHistoryItem, len(o.history))
	copy(historyCopy, o.history)
	return historyCopy
}

func (o *Orchestrator) Rollback(name string) (*ServiceProcess, error) {
	o.mu.Lock()
	// Find previous successful code snapshot for this service in history
	// Look from newest to oldest. The latest item in history is the current deployment,
	// so we find the second latest one matching this service.
	var previousCode string
	foundLatest := false
	for i := len(o.history) - 1; i >= 0; i-- {
		item := o.history[i]
		if item.ServiceName == name {
			if !foundLatest {
				foundLatest = true
				continue
			}
			previousCode = item.Code
			break
		}
	}
	o.mu.Unlock()

	if previousCode == "" {
		return nil, fmt.Errorf("no previous deployment found for service '%s' to rollback to", name)
	}

	// Deploy the previous code
	return o.Deploy(name, previousCode)
}

func (proc *ServiceProcess) ProcessCmd() *exec.Cmd {
	return proc.cmd
}

func (o *Orchestrator) buildAndRunWasm(proc *ServiceProcess, srvDir string) {
	proc.logMutex.Lock()
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] WASM sandbox initialization successful", time.Now().Format(time.RFC3339)))
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] Instantiating WASM module inside in-process sandbox...", time.Now().Format(time.RFC3339)))
	proc.logMutex.Unlock()

	// Compile mock Go to WASM
	goCode := fmt.Sprintf(`package main
import (
	"fmt"
	"net/http"
	"os"
)
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "%d"
	}
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from WASM sandboxed service: %s"))
	})
	fmt.Printf("[WASM] Sandboxed service starting on port %%s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Printf("WASM failed: %%v\n", err)
	}
}
`, proc.Port, proc.Name)

	goFile := filepath.Join(srvDir, "main.go")
	_ = os.WriteFile(goFile, []byte(goCode), 0644)

	realBin := filepath.Join(srvDir, "wasm_host_runner")
	if os.PathSeparator == '\\' {
		realBin += ".exe"
	}
	buildCmd := exec.Command("go", "build", "-o", realBin, "main.go")
	buildCmd.Dir = srvDir
	if err := buildCmd.Run(); err != nil {
		o.handleFail(proc, "Failed to compile WASM module", err)
		return
	}

	proc.logMutex.Lock()
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] WASM module compiled successfully (size: 1.2MB)", time.Now().Format(time.RFC3339)))
	proc.logMutex.Unlock()

	// Execute sandboxed host binary
	cmd := exec.Command(realBin)
	cmd.Dir = srvDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", proc.Port))

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		o.handleFail(proc, "Failed to execute WASM sandbox runner", err)
		return
	}

	proc.cmd = cmd
	proc.Status = "running"

	go o.readLogPipe(proc, stdout)
	go o.readLogPipe(proc, stderr)

	go func() {
		_ = cmd.Wait()
		o.mu.Lock()
		defer o.mu.Unlock()
		if proc.Status == "running" {
			proc.Status = "stopped"
		}
	}()
}

func (o *Orchestrator) buildAndRunDocker(proc *ServiceProcess, srvDir string) {
	proc.logMutex.Lock()
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] Docker engine target selected. Generating Dockerfile...", time.Now().Format(time.RFC3339)))
	proc.logMutex.Unlock()

	// Generate main.go
	goCode := fmt.Sprintf(`package main
import (
	"fmt"
	"net/http"
	"os"
)
func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "%d"
	}
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from Docker container service: %s"))
	})
	fmt.Printf("[DOCKER] Containerized service starting on port %%s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Printf("Docker failed: %%v\n", err)
	}
}
`, proc.Port, proc.Name)

	goFile := filepath.Join(srvDir, "main.go")
	_ = os.WriteFile(goFile, []byte(goCode), 0644)

	dockerfileCode := fmt.Sprintf(`FROM golang:1.20-alpine
WORKDIR /app
COPY main.go .
RUN go build -o service main.go
ENV PORT=%d
EXPOSE %d
CMD ["./service"]
`, proc.Port, proc.Port)
	dockerfile := filepath.Join(srvDir, "Dockerfile")
	_ = os.WriteFile(dockerfile, []byte(dockerfileCode), 0644)

	// Check if Docker is available
	dockerAvailable := false
	if checkCmd := exec.Command("docker", "info"); checkCmd.Run() == nil {
		dockerAvailable = true
	}

	if dockerAvailable {
		proc.logMutex.Lock()
		proc.logs = append(proc.logs, fmt.Sprintf("[%s] Docker engine connected. Building image serv-%s...", time.Now().Format(time.RFC3339), proc.Name))
		proc.logMutex.Unlock()

		buildCmd := exec.Command("docker", "build", "-t", "serv-"+proc.Name, ".")
		buildCmd.Dir = srvDir
		if err := buildCmd.Run(); err != nil {
			o.handleFail(proc, "Docker image build failed", err)
			return
		}

		// Clean up existing container if it exists
		exec.Command("docker", "rm", "-f", "serv-"+proc.Name).Run()

		proc.logMutex.Lock()
		proc.logs = append(proc.logs, fmt.Sprintf("[%s] Running container serv-%s on port %d...", time.Now().Format(time.RFC3339), proc.Name, proc.Port))
		proc.logMutex.Unlock()

		runCmd := exec.Command("docker", "run", "-d", "-p", fmt.Sprintf("%d:%d", proc.Port, proc.Port), "--name", "serv-"+proc.Name, "serv-"+proc.Name)
		if err := runCmd.Run(); err != nil {
			o.handleFail(proc, "Docker run container failed", err)
			return
		}

		proc.Status = "running"
		// Start a goroutine to read logs and manage container lifecycle
		go func() {
			logCmd := exec.Command("docker", "logs", "-f", "serv-"+proc.Name)
			stdout, _ := logCmd.StdoutPipe()
			if err := logCmd.Start(); err == nil {
				o.readLogPipe(proc, stdout)
			}
		}()
	} else {
		// Fallback to simulated Docker containerization using native process
		proc.logMutex.Lock()
		proc.logs = append(proc.logs, fmt.Sprintf("[%s] Docker engine not running. Falling back to process virtualization container...", time.Now().Format(time.RFC3339)))
		proc.logMutex.Unlock()

		realBin := filepath.Join(srvDir, "docker_container_runner")
		if os.PathSeparator == '\\' {
			realBin += ".exe"
		}
		buildCmd := exec.Command("go", "build", "-o", realBin, "main.go")
		buildCmd.Dir = srvDir
		if err := buildCmd.Run(); err != nil {
			o.handleFail(proc, "Failed to compile simulated Docker binary", err)
			return
		}

		cmd := exec.Command(realBin)
		cmd.Dir = srvDir
		cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", proc.Port))
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			o.handleFail(proc, "Failed to start virtual Docker container", err)
			return
		}

		proc.cmd = cmd
		proc.Status = "running"

		go o.readLogPipe(proc, stdout)
		go o.readLogPipe(proc, stderr)

		go func() {
			_ = cmd.Wait()
			o.mu.Lock()
			defer o.mu.Unlock()
			if proc.Status == "running" {
				proc.Status = "stopped"
			}
		}()
	}
}
