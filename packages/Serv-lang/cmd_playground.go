package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type PlaygroundRunRequest struct {
	Source string `json:"source"`
}

type PlaygroundRunResponse struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

var (
	pgServExePath string
	pgRepoRoot    string
)

func findPgRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

func initPlaygroundVars() {
	pgRepoRoot = findPgRepoRoot()
	candidates := []string{
		filepath.Join(pgRepoRoot, "serv.exe"),
		filepath.Join(pgRepoRoot, "github.com/vyuvaraj/serv/packages/Serv-lang"),
	}

	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				pgServExePath = abs
				break
			}
		}
	}
}

// runPlayground starts the web playground server (DX.24)
func runPlayground() {
	initPlaygroundVars()
	port := 8095 // use a default port that doesn't conflict with main 8080

	for i := 2; i < len(os.Args); i++ {
		if (os.Args[i] == "--port" || os.Args[i] == "-port" || os.Args[i] == "-p") && i+1 < len(os.Args) {
			fmt.Sscanf(os.Args[i+1], "%d", &port)
			i++
		}
	}

	// Serve static files
	uiPath := filepath.Join(pgRepoRoot, "web_playground", "ui")
	fs := http.FileServer(http.Dir(uiPath))
	
	mux := http.NewServeMux()
	mux.Handle("/", fs)
	mux.HandleFunc("/api/run", handlePgRun)

	fmt.Printf("🚀 Serv Playground Web IDE started at http://localhost:%d\n", port)
	if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux); err != nil {
		log.Fatalf("Playground server failed to start: %v", err)
	}
}

func handlePgRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(PlaygroundRunResponse{Success: false, Error: "Method not allowed"})
		return
	}

	var req PlaygroundRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(PlaygroundRunResponse{Success: false, Error: "Invalid JSON request"})
		return
	}

	// Create a temporary sandbox directory for this run
	randBytes := make([]byte, 8)
	rand.Read(randBytes)
	runId := hex.EncodeToString(randBytes)

	tempDir := filepath.Join(os.TempDir(), "serv_playground_"+runId)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(PlaygroundRunResponse{Success: false, Error: "Failed to create sandbox"})
		return
	}
	defer os.RemoveAll(tempDir)

	sourceFile := filepath.Join(tempDir, "main.srv")
	if err := os.WriteFile(sourceFile, []byte(req.Source), 0644); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(PlaygroundRunResponse{Success: false, Error: "Failed to write source file"})
		return
	}

	// Compile & run the code using compiler binary or go run
	var cmd *exec.Cmd
	if pgServExePath != "" {
		cmd = exec.Command(pgServExePath, "run", sourceFile)
	} else {
		mainGo := filepath.Join(pgRepoRoot, "main.go")
		cmd = exec.Command("go", "run", mainGo, "run", sourceFile)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run with a 5-second timeout
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(PlaygroundRunResponse{Success: false, Error: "Failed to execute compiler"})
		return
	}

	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(PlaygroundRunResponse{
			Success: false,
			Output:  stdout.String(),
			Error:   "Execution timed out after 5 seconds",
		})
	case err := <-done:
		if err != nil {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(PlaygroundRunResponse{
				Success: false,
				Output:  stdout.String(),
				Error:   stderr.String(),
			})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(PlaygroundRunResponse{
				Success: true,
				Output:  stdout.String(),
			})
		}
	}
}
