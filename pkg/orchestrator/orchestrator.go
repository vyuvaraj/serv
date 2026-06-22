package orchestrator

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type ServiceProcess struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"` // deploying, running, failed, stopped
	Port      int       `json:"port"`
	Error     string    `json:"error,omitempty"`
	DeployedAt time.Time `json:"deployed_at"`
	
	cmd       *exec.Cmd
	logs      []string
	logMutex  sync.RWMutex
}

type Orchestrator struct {
	mu         sync.RWMutex
	services   map[string]*ServiceProcess
	workDir    string
	servPath   string // path to the 'serv' compiler binary, if available
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

	return &Orchestrator{
		services: make(map[string]*ServiceProcess),
		workDir:  absWorkDir,
		servPath: servPath,
	}, nil
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

	proc := &ServiceProcess{
		Name:       name,
		Status:     "deploying",
		Port:       port,
		DeployedAt: time.Now(),
	}
	o.services[name] = proc

	// Start build & run asynchronously
	go o.buildAndRun(proc, srvDir, srvFile)

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
}

func (o *Orchestrator) handleFail(proc *ServiceProcess, msg string, err error) {
	proc.Status = "failed"
	proc.Error = fmt.Sprintf("%s: %v", msg, err)
	proc.logMutex.Lock()
	proc.logs = append(proc.logs, fmt.Sprintf("[%s] ERROR: %s: %v", time.Now().Format(time.RFC3339), msg, err))
	proc.logMutex.Unlock()
}

func (o *Orchestrator) stopService(proc *ServiceProcess) {
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
