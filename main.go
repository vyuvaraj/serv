package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/vyuvaraj/ServShared"
	"servregistry/pkg/registry"
	"servregistry/pkg/web"
)

// initS3 wires the global registry.S3Client to the given endpoint URL.
// Used in tests to point S3 at the local mock server.
func initS3(endpoint string) (*s3.Client, error) {
	resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL:               endpoint,
			SigningRegion:     "us-east-1",
			HostnameImmutable: true,
		}, nil
	})
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithEndpointResolverWithOptions(resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})
	registry.ActiveStore = &registry.S3Store{Client: client}
	return client, nil
}

func startMockS3Server() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to start local S3 mock: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	_ = os.MkdirAll("./packages", 0755)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		trimmed := strings.TrimPrefix(path, "/serv-packages")
		localPath := filepath.Join("./packages", trimmed)

		if r.Method == "PUT" {
			if trimmed == "" || trimmed == "/" {
				w.WriteHeader(http.StatusOK)
				return
			}
			_ = os.MkdirAll(filepath.Dir(localPath), 0755)
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			err = os.WriteFile(localPath, data, 0644)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(http.StatusCreated)
			return
		}

		if r.Method == "GET" {
			if r.URL.Query().Has("list-type") {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				
				var contentsXml []string
				_ = filepath.Walk("./packages", func(p string, info os.FileInfo, err error) error {
					if err == nil && !info.IsDir() {
						rel, _ := filepath.Rel("./packages", p)
						rel = filepath.ToSlash(rel)
						contentsXml = append(contentsXml, fmt.Sprintf("<Contents><Key>%s</Key><Size>%d</Size></Contents>", rel, info.Size()))
					}
					return nil
				})

				w.Write(fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>serv-packages</Name><IsTruncated>false</IsTruncated>%s</ListBucketResult>`, strings.Join(contentsXml, "")))
				return
			}

			data, err := os.ReadFile(localPath)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(data)
			return
		}

		if r.Method == "HEAD" {
			w.WriteHeader(http.StatusOK)
			return
		}
	})

	go http.Serve(listener, mux)
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

//go:embed web/*
var webAssets embed.FS

func main() {
	var addr string
	portStr := flag.String("port", "8088", "Registry server listen address")
	s3Endpoint := flag.String("s3-endpoint", "http://localhost:9000", "ServStore/S3 endpoint URL")
	s3AccessKey := flag.String("s3-access-key", "admin", "S3 access key")
	s3SecretKey := flag.String("s3-secret-key", "admin123", "S3 secret key")
	ociEndpoint := flag.String("oci-endpoint", "", "OCI Registry endpoint URL (enables OCI backend)")
	ociUser := flag.String("oci-username", "", "OCI Registry username")
	ociPass := flag.String("oci-password", "", "OCI Registry password")
	flag.Parse()

	registry.AclStore = registry.NewACLStore("acls.json")

	if envPort := os.Getenv("PORT"); envPort != "" {
		addr = ":" + envPort
	} else {
		addr = ":" + *portStr
	}
	if envEndpoint := os.Getenv("SERV_STORE_ENDPOINT"); envEndpoint != "" {
		*s3Endpoint = envEndpoint
	}
	if envAccessKey := os.Getenv("SERV_STORE_ACCESS_KEY"); envAccessKey != "" {
		*s3AccessKey = envAccessKey
	}
	if envSecretKey := os.Getenv("SERV_STORE_SECRET_KEY"); envSecretKey != "" {
		*s3SecretKey = envSecretKey
	}

	envOciEndpoint := os.Getenv("OCI_REGISTRY_URL")
	if envOciEndpoint == "" {
		envOciEndpoint = *ociEndpoint
	}
	envOciUser := os.Getenv("OCI_REGISTRY_USERNAME")
	if envOciUser == "" {
		envOciUser = *ociUser
	}
	envOciPass := os.Getenv("OCI_REGISTRY_PASSWORD")
	if envOciPass == "" {
		envOciPass = *ociPass
	}

	if envOciEndpoint != "" {
		log.Printf("OCI Package Registry Backend enabled at %s", envOciEndpoint)
		registry.ActiveStore = registry.NewOCIRegistryStore(envOciEndpoint, envOciUser, envOciPass)
	} else {
		standalone := ServShared.IsStandalone()
		if standalone {
			mockEndpoint := startMockS3Server()
			log.Printf("ServRegistry: Running in standalone mode. Redirecting package storage to local packages/ directory via mock S3 at %s.", mockEndpoint)
			*s3Endpoint = mockEndpoint
		} else {
			log.Printf("Connecting to ServStore S3 at %s...", *s3Endpoint)
		}

		customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:               *s3Endpoint,
				SigningRegion:     "us-east-1",
				HostnameImmutable: true,
			}, nil
		})

		cfg, err := config.LoadDefaultConfig(context.Background(),
			config.WithEndpointResolverWithOptions(customResolver),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(*s3AccessKey, *s3SecretKey, "")),
		)
		if err != nil {
			log.Fatalf("Unable to load S3 SDK config: %v", err)
		}

		registry.S3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.UsePathStyle = true
		})

		registry.ActiveStore = &registry.S3Store{Client: registry.S3Client}
		registry.EnsureBucketExists(context.Background())
	}

	go registry.BuildPackageIndex(context.Background())

	web.SetWebAssets(webAssets)
	web.InitSchemas()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/readyz", ServShared.ReadyzHandler)
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servregistry", "1.0.0"))

	mux.HandleFunc("/publish", web.HandlePublish)
	mux.HandleFunc("/api/v1/publish", web.HandlePublish)
	mux.HandleFunc("/packages/", web.HandleGetPackage)
	mux.HandleFunc("/api/v1/packages/", web.HandleGetPackage)
	mux.HandleFunc("/api/v1/packages/provenance/", web.HandleGetProvenance)
	mux.HandleFunc("/api/packages/search", web.HandleSearchPackages)
	mux.HandleFunc("/api/v1/packages/search", web.HandleSearchPackages)
	mux.HandleFunc("/api/packages/", web.HandlePackagesAPI)
	mux.HandleFunc("/api/v1/registry/", web.HandlePackagesAPI)
	mux.HandleFunc("/api/v1/schemas/", web.HandleSchemasAPI)
	mux.HandleFunc("/api/v1/schemas/validate", web.HandleSchemaValidationAPI)
	mux.HandleFunc("/api/v1/marketplace/list", web.HandleMarketplaceList)
	mux.HandleFunc("/api/v1/marketplace/publish", web.HandleMarketplacePublish)
	mux.HandleFunc("/", web.HandleWebDashboard)

	log.Printf("ServRegistry running on http://localhost%s", addr)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Registry: Shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Registry: Server forced to shutdown: %v", err)
	} else {
		log.Println("Registry: Server exited cleanly")
	}
}
