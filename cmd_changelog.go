package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

func runChangelog() {
	changelogCmd := flag.NewFlagSet("changelog", flag.ExitOnError)
	versionFilter := changelogCmd.String("version", "", "Filter changelog by version (e.g. 1.7.0)")
	serviceFilter := changelogCmd.String("service", "", "Filter changelog by service name (e.g. ServGate, ServShared, ServFlow)")

	if len(os.Args) > 2 {
		changelogCmd.Parse(os.Args[2:])
	}

	possiblePaths := []string{
		"../servverse/CHANGELOG.md",
		"../../servverse/CHANGELOG.md",
		"f:/Don/servverse/servverse/CHANGELOG.md",
		"CHANGELOG.md",
	}

	var changelogPath string
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			changelogPath = p
			break
		}
	}

	if changelogPath == "" {
		fmt.Println("❌ Error: Ecosystem CHANGELOG.md not found in standard paths.")
		os.Exit(1)
	}

	file, err := os.Open(changelogPath)
	if err != nil {
		fmt.Printf("❌ Error opening changelog: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	fmt.Printf("📖 Showing Servverse Changelog (Source: %s)\n", changelogPath)
	if *versionFilter != "" {
		fmt.Printf("🔍 Filtering by version: %s\n", *versionFilter)
	}
	if *serviceFilter != "" {
		fmt.Printf("🔍 Filtering by service: %s\n", *serviceFilter)
	}
	fmt.Println("---")

	scanner := bufio.NewScanner(file)
	var currentVersion string
	var versionLines []string
	inTargetVersion := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## [") {
			if len(versionLines) > 0 {
				printVersionLines(currentVersion, versionLines, *serviceFilter)
			}
			versionLines = nil

			closeBrac := strings.Index(trimmed, "]")
			if closeBrac > 4 {
				currentVersion = trimmed[4:closeBrac]
			} else {
				currentVersion = trimmed
			}

			if *versionFilter == "" || currentVersion == *versionFilter {
				inTargetVersion = true
			} else {
				inTargetVersion = false
			}
			continue
		}

		if inTargetVersion || *versionFilter == "" {
			if trimmed != "" || len(versionLines) > 0 {
				versionLines = append(versionLines, line)
			}
		}
	}

	if len(versionLines) > 0 {
		printVersionLines(currentVersion, versionLines, *serviceFilter)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading changelog: %v\n", err)
	}
}

func printVersionLines(version string, lines []string, serviceFilter string) {
	if len(lines) == 0 {
		return
	}

	var filtered []string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "###") {
			filtered = append(filtered, line)
			continue
		}
		if serviceFilter != "" {
			if strings.Contains(strings.ToLower(line), strings.ToLower(serviceFilter)) {
				filtered = append(filtered, line)
			}
		} else {
			filtered = append(filtered, line)
		}
	}

	hasContent := false
	for _, l := range filtered {
		if !strings.HasPrefix(strings.TrimSpace(l), "###") && strings.TrimSpace(l) != "" {
			hasContent = true
			break
		}
	}

	if hasContent {
		fmt.Printf("\n## Version %s\n", version)
		for _, l := range filtered {
			fmt.Println(l)
		}
	}
}
