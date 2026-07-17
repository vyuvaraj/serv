package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"serv/compiler"
)

func runDocs() {
	// Support both:
	//   serv doc <file.srv> [-o <out.html>]  (HTML output, default)
	//   serv docs generate <file.srv> [-o <out.json>] (JSON output)
	//   serv docs serve <file.srv> [--port <port>] [--watch] (live local preview)
	
	cmd := os.Args[1] // "doc" or "docs"
	
	if cmd == "docs" {
		if len(os.Args) < 3 {
			fmt.Println("Usage:")
			fmt.Println("  serv docs generate <file.srv> [-o <output-file>]")
			fmt.Println("  serv docs serve <file.srv> [--port <port>] [--watch]")
			os.Exit(1)
		}
		subCommand := os.Args[2]
		if subCommand == "serve" {
			serveCmd := flag.NewFlagSet("docs serve", flag.ExitOnError)
			portFlag := serveCmd.Int("port", 8010, "Port to serve documentation on")
			watchFlag := serveCmd.Bool("watch", false, "Watch for changes in .srv files and reload")

			var options []string
			var fileArg string
			for i := 3; i < len(os.Args); i++ {
				arg := os.Args[i]
				if (arg == "--port" || arg == "-port") && i+1 < len(os.Args) {
					options = append(options, "-port", os.Args[i+1])
					i++
				} else if arg == "--watch" || arg == "-watch" {
					options = append(options, "-watch")
				} else if strings.HasPrefix(arg, "-") {
					options = append(options, arg)
				} else {
					fileArg = arg
				}
			}

			if fileArg == "" {
				fmt.Println("Usage: serv docs serve <file.srv> [--port <port>] [--watch]")
				os.Exit(1)
			}

			if err := serveCmd.Parse(options); err != nil {
				fmt.Printf("Error parsing options: %v\n", err)
				os.Exit(1)
			}

			serveDocs(fileArg, *portFlag, *watchFlag)
			return
		}

		if subCommand != "generate" {
			fmt.Printf("Unknown docs subcommand: %s. Did you mean 'generate' or 'serve'?\n", subCommand)
			os.Exit(1)
		}

		docsCmd := flag.NewFlagSet("docs generate", flag.ExitOnError)
		outputFile := docsCmd.String("o", "openapi.json", "Output file path")

		var options []string
		var fileArg string
		for i := 3; i < len(os.Args); i++ {
			arg := os.Args[i]
			if arg == "-o" && i+1 < len(os.Args) {
				options = append(options, "-o", os.Args[i+1])
				i++
			} else if strings.HasPrefix(arg, "-") {
				options = append(options, arg)
			} else {
				fileArg = arg
			}
		}

		if fileArg == "" {
			fmt.Println("Usage: serv docs generate <file.srv> [-o <output-file>]")
			os.Exit(1)
		}

		if err := docsCmd.Parse(options); err != nil {
			fmt.Printf("Error parsing options: %v\n", err)
			os.Exit(1)
		}

		_, prog, err := parseProject(fileArg)
		if err != nil {
			fmt.Printf("Error parsing project: %v\n", err)
			os.Exit(1)
		}

		jsonStr, err := compiler.GenerateOpenAPI(prog)
		if err != nil {
			fmt.Printf("Error generating OpenAPI: %v\n", err)
			os.Exit(1)
		}

		if err := os.WriteFile(*outputFile, []byte(jsonStr), 0644); err != nil {
			fmt.Printf("Error writing output file: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✓ Successfully generated OpenAPI documentation at %s\n", *outputFile)
		return
	}

	// Else: serv doc <file.srv> [-o <out.html>]
	docCmd := flag.NewFlagSet("doc", flag.ExitOnError)
	outputFile := docCmd.String("o", "docs.html", "Output file path")

	var options []string
	var fileArg string
	for i := 2; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "-o" && i+1 < len(os.Args) {
			options = append(options, "-o", os.Args[i+1])
			i++
		} else if strings.HasPrefix(arg, "-") {
			options = append(options, arg)
		} else {
			fileArg = arg
		}
	}

	if fileArg == "" {
		fmt.Println("Usage: serv doc <file.srv> [-o <output-file.html>]")
		os.Exit(1)
	}

	if err := docCmd.Parse(options); err != nil {
		fmt.Printf("Error parsing options: %v\n", err)
		os.Exit(1)
	}

	_, prog, err := parseProject(fileArg)
	if err != nil {
		fmt.Printf("Error parsing project: %v\n", err)
		os.Exit(1)
	}

	// Extract the source code comments if any, but since the compiler does not preserve them in AST,
	// we will scan the srv source files and compile-doc them.
	// For now, let's call GenerateHTMLDocs
	htmlStr, err := compiler.GenerateHTMLDocs(prog, fileArg)
	if err != nil {
		fmt.Printf("Error generating HTML documentation: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outputFile, []byte(htmlStr), 0644); err != nil {
		fmt.Printf("Error writing output file: %v\n", err)
		os.Exit(1)
	}

	absPath, _ := filepath.Abs(*outputFile)
	fmt.Printf("✓ Successfully generated HTML documentation at %s\n", absPath)
}

func serveDocs(fileArg string, port int, watch bool) *http.Server {
	// Live reload channel
	reloadChan := make(chan struct{}, 10)

	// Pre-build the HTML
	var htmlContent string
	var mu sync.Mutex

	rebuild := func() {
		mu.Lock()
		defer mu.Unlock()

		_, prog, err := parseProject(fileArg)
		if err != nil {
			fmt.Printf("[Docs Compiler] Error parsing project: %v\n", err)
			return
		}

		htmlStr, err := compiler.GenerateHTMLDocs(prog, fileArg)
		if err != nil {
			fmt.Printf("[Docs Compiler] Error generating HTML: %v\n", err)
			return
		}

		// Inject live-reload script
		if watch {
			injectScript := `
<script>
  const evtSource = new EventSource("/events");
  evtSource.onmessage = function(event) {
      if (event.data === "reload") {
          console.log("File change detected. Reloading...");
          window.location.reload();
      }
  };
</script>
</body>`
			htmlStr = strings.Replace(htmlStr, "</body>", injectScript, 1)
		}
		htmlContent = htmlStr
	}

	// Initial compile
	rebuild()

	// File watcher (if watch is enabled)
	if watch {
		go func() {
			dir := filepath.Dir(fileArg)
			lastModTimes := make(map[string]time.Time)
			
			scanFiles := func() bool {
				changed := false
				currentFiles := make(map[string]bool)
				
				_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					if strings.HasSuffix(info.Name(), ".srv") {
						currentFiles[path] = true
						oldTime, exists := lastModTimes[path]
						if !exists || info.ModTime().After(oldTime) {
							lastModTimes[path] = info.ModTime()
							changed = true
						}
					}
					return nil
				})

				for path := range lastModTimes {
					if !currentFiles[path] {
						delete(lastModTimes, path)
						changed = true
					}
				}
				return changed
			}

			_ = scanFiles()

			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			for range ticker.C {
				if scanFiles() {
					fmt.Println("Changes detected in .srv files. Rebuilding docs...")
					rebuild()
					select {
					case reloadChan <- struct{}{}:
					default:
					}
				}
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		content := htmlContent
		mu.Unlock()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(content))
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "data: connected\n\n")
		flusher.Flush()

		for {
			select {
			case <-reloadChan:
				fmt.Fprintf(w, "data: reload\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("Documentation server started at http://%s\n", addr)
	if watch {
		fmt.Println("File watcher active: automatically reloading on changes to `.srv` files.")
	}
	
	server := &http.Server{Addr: addr, Handler: mux}
	// Return server instance to allow clean closures/shutdowns in tests
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Error starting documentation server: %v\n", err)
			os.Exit(1)
		}
	}()
	// Sleep briefly to ensure the port binding is ready
	time.Sleep(50 * time.Millisecond)
	return server
}

