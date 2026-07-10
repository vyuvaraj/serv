package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"crypto/tls"
	"crypto/x509"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"servmesh/pkg/registry"
)

const version = "0.1.0"

func main() {
	// Subcommand routing
	if len(os.Args) > 1 && os.Args[0] != "inspect" {
		switch os.Args[1] {
		case "inspect":
			fmt.Fprintln(os.Stderr, "Run the inspect tool via: go run ./cmd/inspect/ [--registry <url>] [--service <name>] [--watch]")
			os.Exit(0)
		case "version":
			fmt.Printf("ServMesh v%s\n", version)
			os.Exit(0)
		case "help":
			fmt.Printf("ServMesh v%s\n\n", version)
			fmt.Println("Subcommands:")
			fmt.Println("  (default)  Start the ServMesh registry daemon")
			fmt.Println("  up         Start local registry (:8089) and transparent proxy (:8090)")
			fmt.Println("  inspect    Show live service topology (run via: go run ./cmd/inspect/)")
			fmt.Println("  version    Print version and exit")
			fmt.Println("\nRegistry flags:")
			fmt.Println("  --port <n>   Listen port (default 8089)")
			fmt.Println("  --ttl  <n>   Heartbeat TTL in seconds (default 10)")
			fmt.Println("\n'up' Subcommand flags:")
			fmt.Println("  --port <n>         Registry listen port (default 8089)")
			fmt.Println("  --proxy-port <n>   Proxy listen port (default 8090)")
			fmt.Println("  --ttl <n>          Heartbeat TTL in seconds (default 10)")
			os.Exit(0)
		}
	}

	// Check if running "up" subcommand
	if len(os.Args) > 1 && os.Args[1] == "up" {
		upCmd := flag.NewFlagSet("up", flag.ExitOnError)
		port := upCmd.Int("port", 8089, "Registry listen port")
		proxyPort := upCmd.Int("proxy-port", 8090, "Proxy listen port")
		ttlSec := upCmd.Int("ttl", 10, "Service instance heartbeat TTL in seconds")
		upCmd.Parse(os.Args[2:])

		log.Printf("Starting Local Dev Service Mesh v%s...", version)
		log.Printf("Registry listening on :%d", *port)
		log.Printf("Proxy listening on :%d", *proxyPort)

		r := registry.NewRegistry(time.Duration(*ttlSec) * time.Second)

		regServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", *port),
			Handler: r.Handler(),
		}
		startServer(regServer)

		proxy := &devProxy{reg: r}
		proxyServer := &http.Server{
			Addr:    fmt.Sprintf(":%d", *proxyPort),
			Handler: proxy,
		}
		startServer(proxyServer)

		stopChan := make(chan os.Signal, 1)
		signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
		<-stopChan

		log.Println("Shutting down Local Dev Service Mesh gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		regServer.Shutdown(shutdownCtx)
		proxyServer.Shutdown(shutdownCtx)
		r.Close()
		log.Println("Local Dev Service Mesh shutdown complete.")
		return
	}

	port := flag.Int("port", 8089, "Registry listen port")
	ttlSec := flag.Int("ttl", 10, "Service instance heartbeat TTL in seconds")
	verFlag := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *verFlag {
		fmt.Printf("ServMesh Registry v%s\n", version)
		return
	}

	log.Printf("Starting ServMesh Registry v%s on port :%d...", version, *port)
	
	r := registry.NewRegistry(time.Duration(*ttlSec) * time.Second)
	
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: r.Handler(),
	}

	startServer(server)

	// Graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	log.Println("Shutting down ServMesh Registry gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Registry forced shutdown: %v", err)
	}

	r.Close()
	log.Println("ServMesh Registry shutdown complete.")
}

type devProxy struct {
	reg *registry.Registry
}

func (p *devProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var serviceName string
	var subPath string = r.URL.Path

	if svc := r.Header.Get("X-Mesh-Service"); svc != "" {
		serviceName = svc
	} else {
		hostParts := strings.Split(r.Host, ":")
		if len(hostParts) > 0 && hostParts[0] != "localhost" && hostParts[0] != "127.0.0.1" {
			serviceName = hostParts[0]
		} else {
			path := strings.TrimPrefix(r.URL.Path, "/")
			parts := strings.SplitN(path, "/", 2)
			if len(parts) > 0 && parts[0] != "" {
				serviceName = parts[0]
				if len(parts) == 2 {
					subPath = "/" + parts[1]
				} else {
					subPath = "/"
				}
			}
		}
	}

	if serviceName == "" {
		http.Error(w, "dev-mesh proxy error: could not determine service name from Host, Path prefix, or X-Mesh-Service header", http.StatusBadRequest)
		return
	}

	instances := p.reg.Resolve(serviceName)
	if len(instances) == 0 {
		http.Error(w, fmt.Sprintf("dev-mesh proxy error: service %q not found in local registry", serviceName), http.StatusNotFound)
		return
	}

	inst := instances[0]
	if len(instances) > 1 {
		inst = instances[time.Now().UnixNano()%int64(len(instances))]
	}

	targetURL, err := url.Parse(inst.Address)
	if err != nil {
		http.Error(w, fmt.Sprintf("dev-mesh proxy error: invalid address %q: %v", inst.Address, err), http.StatusInternalServerError)
		return
	}

	rp := httputil.NewSingleHostReverseProxy(targetURL)
	r.URL.Path = subPath
	r.URL.RawPath = ""
	r.Host = targetURL.Host

	rp.ServeHTTP(w, r)
}

func startServer(srv *http.Server) {
	if os.Getenv("SERVMESH_MTLS_REQUIRED") == "true" {
		certFile := os.Getenv("SERVMESH_CERT")
		keyFile := os.Getenv("SERVMESH_KEY")
		caFile := os.Getenv("SERVMESH_CACERT")

		if certFile != "" && keyFile != "" {
			tlsConfig := &tls.Config{
				ClientAuth: tls.RequireAndVerifyClientCert,
			}
			if caFile != "" {
				caCert, err := os.ReadFile(caFile)
				if err == nil {
					caCertPool := x509.NewCertPool()
					caCertPool.AppendCertsFromPEM(caCert)
					tlsConfig.ClientCAs = caCertPool
				}
			}
			srv.TLSConfig = tlsConfig
			log.Printf("[ServMesh] Enforcing mutual TLS on address %s", srv.Addr)
			go func() {
				if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
					log.Fatalf("Server failed under mTLS: %v", err)
				}
			}()
			return
		}
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()
}

