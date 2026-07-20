package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
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

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("Failed to create fsnotify watcher: %v\n", err)
		return
	}
	defer watcher.Close()

	// Recursive directory watch registration
	addWatcherDirs := func(root string) {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				// Skip common build or vcs directories
				base := filepath.Base(path)
				if base == ".git" || base == ".build" || base == "node_modules" {
					return filepath.SkipDir
				}
				_ = watcher.Add(path)
			}
			return nil
		})
	}

	addWatcherDirs(targetPath)

	// Debounce triggers
	var (
		mu           sync.Mutex
		lastTrigger  time.Time
		triggerDelay = 200 * time.Millisecond
	)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Watch for writes or creates of .srv files
			if strings.HasSuffix(event.Name, ".srv") && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
				mu.Lock()
				now := time.Now()
				if now.Sub(lastTrigger) > triggerDelay {
					lastTrigger = now
					fmt.Printf("\n[WATCH MODE] Change detected: %s. Re-running tests...\n", filepath.Base(event.Name))
					run()
				}
				mu.Unlock()
			}

			// Add dynamically created directories to watcher
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					addWatcherDirs(event.Name)
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("[WATCH MODE] Watcher error: %v\n", err)
		}
	}
}
