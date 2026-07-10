package main

import (
	"context"
	"embed"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
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

//go:embed web/*
var webAssets embed.FS

func main() {
	addr := flag.String("addr", ":8088", "Registry server listen address")
	s3Endpoint := flag.String("s3-endpoint", "http://localhost:9000", "ServStore/S3 endpoint URL")
	s3AccessKey := flag.String("s3-access-key", "admin", "S3 access key")
	s3SecretKey := flag.String("s3-secret-key", "admin123", "S3 secret key")
	flag.Parse()

	registry.AclStore = registry.NewACLStore("acls.json")

	if envPort := os.Getenv("PORT"); envPort != "" {
		*addr = ":" + envPort
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

	log.Printf("Connecting to ServStore S3 at %s...", *s3Endpoint)

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

	registry.EnsureBucketExists(context.Background())
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

	log.Printf("ServRegistry running on http://localhost%s", *addr)
	server := &http.Server{
		Addr:    *addr,
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
