package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"serv/compiler"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	command := os.Args[1]

	switch command {
	case "build":
		buildCmd := flag.NewFlagSet("build", flag.ExitOnError)
		outputFlag := buildCmd.String("o", "service.exe", "Output binary name")
		if err := buildCmd.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Error parsing arguments: %v\n", err)
			os.Exit(1)
		}
		args := buildCmd.Args()
		if len(args) < 1 {
			fmt.Println("Usage: serv build <file.srv> [-o <output>]")
			os.Exit(1)
		}
		buildServ(args[0], *outputFlag)

	case "run":
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)
		watchFlag := runCmd.Bool("watch", false, "Watch files and hot-reload")
		if err := runCmd.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Error parsing arguments: %v\n", err)
			os.Exit(1)
		}
		args := runCmd.Args()
		if len(args) < 1 {
			fmt.Println("Usage: serv run <file.srv> [--watch]")
			os.Exit(1)
		}

		if *watchFlag {
			runServWatch(args[0])
		} else {
			runServ(args[0])
		}

	case "dockerize":
		dockerCmd := flag.NewFlagSet("dockerize", flag.ExitOnError)
		if err := dockerCmd.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Error parsing arguments: %v\n", err)
			os.Exit(1)
		}
		args := dockerCmd.Args()
		if len(args) < 1 {
			fmt.Println("Usage: serv dockerize <file.srv>")
			os.Exit(1)
		}
		dockerizeServ(args[0])

	case "test":
		testCmd := flag.NewFlagSet("test", flag.ExitOnError)
		if err := testCmd.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Error parsing arguments: %v\n", err)
			os.Exit(1)
		}
		args := testCmd.Args()
		if len(args) < 1 {
			fmt.Println("Usage: serv test <file.srv>")
			os.Exit(1)
		}
		runTests(args[0])

	case "lint":
		lintCmd := flag.NewFlagSet("lint", flag.ExitOnError)
		if err := lintCmd.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Error parsing arguments: %v\n", err)
			os.Exit(1)
		}
		args := lintCmd.Args()
		if len(args) < 1 {
			fmt.Println("Usage: serv lint <file.srv>")
			os.Exit(1)
		}
		runLint(args[0])

	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println("Serv: A Programming Language for Background Services")
	fmt.Println("Usage:")
	fmt.Println("  serv build <file.srv> [-o <output_binary>]  Compile Serv code to native binary")
	fmt.Println("  serv run <file.srv> [--watch]              Compile and run Serv code immediately (with optional hot reload)")
	fmt.Println("  serv test <file.srv>                       Run tests defined in a Serv file")
	fmt.Println("  serv lint <file.srv>                       Validate syntax of a Serv file")
	fmt.Println("  serv dockerize <file.srv>                  Generate a Dockerfile for the Serv service")
}

func runLint(srvFile string) {
	absPath, err := filepath.Abs(srvFile)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	_, err = parseWithDependencies(absPath, make(map[string]bool))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func buildServ(srvFile, outputBinary string) string {
	absPath, err := buildServNoExit(srvFile, outputBinary)
	if err != nil {
		fmt.Printf("Build failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Build successful! Native binary generated: %s\n", outputBinary)
	return absPath
}

func buildServNoExit(srvFile, outputBinary string) (string, error) {
	absPath, err := filepath.Abs(srvFile)
	if err != nil {
		return "", err
	}

	program, err := parseWithDependencies(absPath, make(map[string]bool))
	if err != nil {
		return "", err
	}

	codegen := compiler.NewCodegen(program)
	goCode, err := codegen.Generate()
	if err != nil {
		return "", err
	}

	goCode += "\n" + codegen.GenerateMainFunc()

	buildDir := filepath.Join(filepath.Dir(absPath), ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return "", err
	}

	// Remove stale test files from previous test runs
	_ = os.Remove(filepath.Join(buildDir, "service.go"))
	_ = os.Remove(filepath.Join(buildDir, "serv_test.go"))


	genGoFile := filepath.Join(buildDir, "main.go")
	if err := os.WriteFile(genGoFile, []byte(goCode), 0644); err != nil {
		return "", err
	}

	goPath := "C:\\Program Files\\Go\\bin\\go.exe"
	cmd := exec.Command(goPath, "build", "-o", filepath.Join(filepath.Dir(absPath), outputBinary), genGoFile)
	cmd.Dir = filepath.Dir(absPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr.String())
	}

	return filepath.Join(filepath.Dir(absPath), outputBinary), nil
}

func runServ(srvFile string) {
	binPath, err := buildServNoExit(srvFile, "temp_service.exe")
	if err != nil {
		fmt.Printf("Build failed: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(binPath)

	fmt.Printf("Running native service: %s...\n", binPath)
	cmd := exec.Command(binPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Service exited with error: %v\n", err)
		os.Exit(1)
	}
}

func runServWatch(srvFile string) {
	fmt.Printf("Starting Serv in Watch Mode: %s...\n", srvFile)

	var cmd *exec.Cmd

	restart := func() {
		if cmd != nil && cmd.Process != nil {
			fmt.Println("[WATCH] File change detected. Restarting service...")
			cmd.Process.Kill()
			cmd.Wait()
		}

		binPath, err := buildServNoExit(srvFile, "watch_service.exe")
		if err != nil {
			fmt.Printf("[WATCH] Rebuild failed:\n%v\n", err)
			return
		}

		fmt.Printf("[WATCH] Starting service: %s\n", binPath)
		cmd = exec.Command(binPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			fmt.Printf("[WATCH] Failed to start service: %v\n", err)
		}
	}

	restart()
	defer func() {
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	watchDir := filepath.Dir(srvFile)
	lastMods := getFileModTimes(watchDir)

	for {
		time.Sleep(500 * time.Millisecond)
		currentMods := getFileModTimes(watchDir)

		changed := false
		for path, mtime := range currentMods {
			oldTime, exists := lastMods[path]
			if !exists || mtime.After(oldTime) {
				changed = true
				break
			}
		}

		if !changed && len(currentMods) != len(lastMods) {
			changed = true
		}

		if changed {
			lastMods = currentMods
			restart()
		}
	}
}

func runTests(srvFile string) {
	absPath, err := filepath.Abs(srvFile)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	program, err := parseWithDependencies(absPath, make(map[string]bool))
	if err != nil {
		fmt.Printf("Parse error: %v\n", err)
		os.Exit(1)
	}

	cg := compiler.NewCodegen(program)
	goCode, err := cg.Generate()
	if err != nil {
		fmt.Printf("Codegen error: %v\n", err)
		os.Exit(1)
	}

	if !cg.HasTests() {
		fmt.Println("No tests found in", srvFile)
		return
	}

	buildDir := filepath.Join(filepath.Dir(absPath), ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		fmt.Printf("Failed to create build dir: %v\n", err)
		os.Exit(1)
	}

	// Clean stale Go files from previous builds to prevent conflicts
	for _, name := range []string{"main.go", "service.go", "serv_test.go"} {
		_ = os.Remove(filepath.Join(buildDir, name))
	}

	// Write service.go: all generated declarations (functions, init blocks, etc.)
	if err := os.WriteFile(filepath.Join(buildDir, "service.go"), []byte(goCode), 0644); err != nil {
		fmt.Printf("Failed to write service.go: %v\n", err)
		os.Exit(1)
	}

	// Write a minimal main.go stub (go test needs a package main with a main func)
	mainStub := "// Code generated by Serv compiler. DO NOT EDIT.\npackage main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(buildDir, "main.go"), []byte(mainStub), 0644); err != nil {
		fmt.Printf("Failed to write main.go: %v\n", err)
		os.Exit(1)
	}

	// Write serv_test.go
	testCode := cg.GenerateTests()
	testFile := filepath.Join(buildDir, "serv_test.go")
	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		fmt.Printf("Failed to write test file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Running tests from %s...\n", srvFile)
	goPath := "C:\\Program Files\\Go\\bin\\go.exe"
	cmd := exec.Command(goPath, "test", "-v", "./...")
	cmd.Dir = buildDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Tests failed: %v\n", err)
		os.Exit(1)
	}
}

func getFileModTimes(dir string) map[string]time.Time {

	mods := make(map[string]time.Time)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.Contains(path, ".build") ||
			strings.Contains(path, "watch_service.exe") ||
			strings.Contains(path, "service.exe") ||
			strings.Contains(path, "temp_service.exe") {
			return nil
		}
		if !info.IsDir() && (strings.HasSuffix(path, ".srv") || strings.HasSuffix(path, ".py")) {
			mods[path] = info.ModTime()
		}
		return nil
	})
	return mods
}

func dockerizeServ(srvFile string) {
	absPath, err := filepath.Abs(srvFile)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	baseName := filepath.Base(srvFile)
	dockerfileContent := fmt.Sprintf(`# Stage 1: Build the Serv executable
FROM golang:1.26.3-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o serv.exe main.go
RUN ./serv.exe build %s -o service_bin

# Stage 2: Create a minimal production container
FROM alpine:latest
RUN apk --no-cache add ca-certificates python3
WORKDIR /root/
COPY --from=builder /app/service_bin .
COPY --from=builder /app/scripts/ ./scripts/
COPY --from=builder /app/examples/ ./examples/
CMD ["./service_bin"]
`, baseName)

	dockerfilePath := filepath.Join(filepath.Dir(absPath), "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		fmt.Printf("Failed to write Dockerfile: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Dockerfile successfully generated at: %s\n", dockerfilePath)
	fmt.Println("You can now build and run your Serv service using Docker:")
	fmt.Println("  docker build -t serv-service .")
	fmt.Println("  docker run -p 8080:8080 -e PORT=8080 serv-service")
}

func parseWithDependencies(filePath string, visited map[string]bool) (*compiler.Program, error) {
	if visited[filePath] {
		return &compiler.Program{}, nil // Prevent circular imports
	}
	visited[filePath] = true

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	lexer := compiler.NewLexer(string(content))
	parser := compiler.NewParser(lexer)
	program := parser.ParseProgram()

	if len(parser.Errors()) > 0 {
		return nil, fmt.Errorf("errors parsing %s:\n%s", filePath, strings.Join(parser.Errors(), "\n"))
	}

	var mergedStatements []compiler.Statement

	for _, stmt := range program.Statements {
		if imp, ok := stmt.(*compiler.ImportStmt); ok {
			importPath := filepath.Join(filepath.Dir(filePath), imp.Path)
			subProgram, err := parseWithDependencies(importPath, visited)
			if err != nil {
				return nil, err
			}
			mergedStatements = append(mergedStatements, subProgram.Statements...)
		} else {
			mergedStatements = append(mergedStatements, stmt)
		}
	}

	program.Statements = mergedStatements
	return program, nil
}
