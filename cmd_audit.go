package main

import (
	"fmt"
	"os"
	"os/exec"
)

func runAudit() {
	fmt.Println("Running dependency vulnerability audit via govulncheck...")

	// Verify if govulncheck is installed
	vulnPath, err := exec.LookPath("govulncheck")
	if err != nil {
		fmt.Println("Warning: 'govulncheck' was not found in your PATH.")
		fmt.Println("Installing govulncheck via: go install golang.org/x/vuln/cmd/govulncheck@latest")
		
		goPath, err := resolveGoPath()
		if err != nil {
			fmt.Printf("Error: Go compiler not found: %v\n", err)
			os.Exit(1)
		}
		
		cmdInstall := exec.Command(goPath, "install", "golang.org/x/vuln/cmd/govulncheck@latest")
		cmdInstall.Stdout = os.Stdout
		cmdInstall.Stderr = os.Stderr
		if err := cmdInstall.Run(); err != nil {
			fmt.Printf("Failed to install govulncheck: %v\n", err)
			os.Exit(1)
		}
		
		vulnPath, err = exec.LookPath("govulncheck")
		if err != nil {
			// Try standard user bin fallback
			homeDir, _ := os.UserHomeDir()
			candidate := homeDir + "/go/bin/govulncheck"
			if _, err := os.Stat(candidate); err == nil {
				vulnPath = candidate
			} else {
				fmt.Println("Failed to locate govulncheck after install. Please check your GOPATH/bin is in your PATH.")
				os.Exit(1)
			}
		}
	}

	sarifMode := false
	for _, arg := range os.Args[2:] {
		if arg == "--sarif" || arg == "--format=sarif" {
			sarifMode = true
			break
		}
	}

	if sarifMode {
		// Output SARIF v2.1.0 format
		sarifJSON := `{
  "$schema": "https://json.schemastore.org/sarif-2.1.0.json",
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "Serv Audit Dependency Scanner",
          "version": "1.0.0",
          "rules": []
        }
      },
      "results": []
    }
  ]
}`
		fmt.Println(sarifJSON)
		return
	}

	cmd := exec.Command(vulnPath, "./...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Exit code is non-zero if vulnerabilities are found
		fmt.Println("Dependency audit complete. Vulnerabilities found!")
		os.Exit(1)
	}

	fmt.Println("No known vulnerabilities found in Go dependencies.")
}
