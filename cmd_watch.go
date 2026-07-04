package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runTestsWatch(targetPath string, cover bool, filter string, integration bool) {
	run := func() {
		if integration {
			runIntegrationTests(targetPath, cover, filter)
		} else {
			runTests(targetPath, cover, filter)
		}
	}

	run()

	fmt.Printf("\n[WATCH MODE] Watching for changes in %s. Press Ctrl+C to exit...\n", targetPath)

	getModTimes := func() map[string]time.Time {
		times := make(map[string]time.Time)
		_ = filepath.Walk(targetPath, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".srv") {
				times[path] = info.ModTime()
			}
			return nil
		})
		return times
	}

	lastModTimes := getModTimes()

	for {
		time.Sleep(500 * time.Millisecond)
		currTimes := getModTimes()
		changed := false

		for path, modTime := range currTimes {
			if lastMod, ok := lastModTimes[path]; !ok || modTime.After(lastMod) {
				changed = true
				break
			}
		}

		if !changed && len(currTimes) != len(lastModTimes) {
			changed = true
		}

		if changed {
			lastModTimes = currTimes
			fmt.Printf("\n[WATCH MODE] Change detected. Re-running tests...\n")
			run()
		}
	}
}
