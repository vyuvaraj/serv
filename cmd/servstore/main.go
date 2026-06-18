package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/cluster"
	"servstore/pkg/otel"
	"servstore/pkg/ratelimit"
	"servstore/pkg/s3"
	"servstore/pkg/storage"
	"servstore/pkg/web"
)

func main() {
	// Initialize default slog logger in structured JSON format
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := flag.Int("port", 8080, "Port to listen on")
	dataDir := flag.String("data-dir", "./data", "Directory to store bucket data and metadata")
	authEnabled := flag.Bool("auth", false, "Enable AWS Signature V4 authentication")
	accessKey := flag.String("access-key", "minioadmin", "AWS Access Key ID")
	secretKey := flag.String("secret-key", "minioadmin", "AWS Secret Access Key")
	encryptionKey := flag.String("encryption-key", "", "Passphrase for AES-256 encryption at rest (empty = disabled)")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file (PEM). Enables HTTPS with TLS 1.3 when set together with --tls-key")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file (PEM). Enables HTTPS with TLS 1.3 when set together with --tls-cert")
	lifecycleInterval := flag.Duration("lifecycle-interval", time.Hour, "How often to run the lifecycle expiry sweep (e.g. 1h, 30m)")
	ldapURL := flag.String("ldap-url", "", "LDAP server URL (e.g., ldap://localhost:389)")
	ldapDNTemplate := flag.String("ldap-dn-template", "", "LDAP DN Template (e.g., cn=%s,ou=users,dc=servstore)")
	oidcIssuer := flag.String("oidc-issuer", "", "OIDC Issuer URL (e.g., https://accounts.google.com)")
	oidcClientID := flag.String("oidc-client-id", "", "OIDC Client ID")
	oidcClientSecret := flag.String("oidc-client-secret", "", "OIDC Client Secret")
	oidcRedirectURI := flag.String("oidc-redirect-uri", "http://localhost:8080/console/oauth/callback", "OIDC Redirect URI")
	nodeID := flag.String("node-id", "", "Unique Node ID (defaults to node-addr)")
	nodeAddr := flag.String("node-addr", "", "Node advertise address (e.g. localhost:8080)")
	peers := flag.String("peers", "", "Comma-separated list of peer addresses (e.g. localhost:8081,localhost:8082)")
	raftPort := flag.Int("raft-port", 8090, "Port for Raft consensus TCP transport")
	raftBootstrap := flag.Bool("raft-bootstrap", false, "Bootstrap this node as the Raft cluster leader")
	replicationFactor := flag.Int("replication-factor", 2, "Number of data replicas to maintain across the cluster")
	erasureCoding := flag.Bool("erasure-coding", false, "Enable Reed-Solomon Erasure Coding instead of replication")
	dataShards := flag.Int("data-shards", 2, "Number of data shards for Erasure Coding")
	parityShards := flag.Int("parity-shards", 1, "Number of parity shards for Erasure Coding")
	rateLimitRPS := flag.Int("rate-limit-rps", 0, "Max requests per second per tenant namespace (0 = unlimited)")
	rateLimitBurst := flag.Int("rate-limit-burst", 0, "Token bucket burst size for rate limiting (defaults to 2×rps when 0)")
	flag.Parse()

	// Initialize OpenTelemetry Tracing (inspired by serv-lang)
	otel.InitOtel("servstore")

	// Ensure data directory is absolute
	absDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		slog.Error("Invalid data directory", "error", err)
		os.Exit(1)
	}

	slog.Info("Starting ServStore Object Storage Platform...", "data-dir", absDataDir)

	// Initialize storage engine
	store, err := storage.NewLocalStore(absDataDir)
	if err != nil {
		slog.Error("Failed to initialize storage engine", "error", err)
		os.Exit(1)
	}
	if *encryptionKey != "" {
		store.WithEncryptionKey(*encryptionKey)
		slog.Info("Encryption at rest", "status", "enabled", "algorithm", "AES-256-GCM")
	} else {
		slog.Info("Encryption at rest", "status", "disabled")
	}

	// Initialize Auth provider
	authProvider := auth.NewAuthProvider(*accessKey, *secretKey, *authEnabled)
	if *authEnabled {
		slog.Info("Authentication status", "enabled", true, "access_key", *accessKey)
	} else {
		slog.Info("Authentication status", "enabled", false, "reason", "Anonymous access allowed")
	}

	// Configure LDAP if URL is set
	if *ldapURL != "" {
		authProvider.ConfigureLDAP(*ldapURL, *ldapDNTemplate)
		slog.Info("LDAP Auth configured", "url", *ldapURL)
	}

	// Configure OIDC if issuer is set
	if *oidcIssuer != "" {
		authProvider.ConfigureOIDC(auth.OIDCConfig{
			Issuer:       *oidcIssuer,
			ClientID:     *oidcClientID,
			ClientSecret: *oidcClientSecret,
			RedirectURI:  *oidcRedirectURI,
		})
		slog.Info("OIDC Auth configured", "issuer", *oidcIssuer)
	}

	// Initialize Cluster Membership & Raft if active
	var clusterMgr *cluster.MembershipManager
	var raftNode *cluster.RaftNode

	if *peers != "" || *nodeAddr != "" {
		addr := *nodeAddr
		if addr == "" {
			addr = fmt.Sprintf("localhost:%d", *port)
		}
		id := *nodeID
		if id == "" {
			id = addr
		}
		clusterMgr = cluster.NewMembershipManager(id, addr, *peers)
		clusterMgr.Start(context.Background())

		raftAddr := fmt.Sprintf("localhost:%d", *raftPort)
		var err error
		raftNode, err = cluster.NewRaftNode(id, raftAddr, store, *raftBootstrap)
		if err != nil {
			slog.Error("Failed to initialize Raft Node", "error", err)
			os.Exit(1)
		}

		if !*raftBootstrap && *peers != "" {
			go func() {
				// Wait a moment for S3 HTTP server to bind
				time.Sleep(2 * time.Second)
				peerList := strings.Split(*peers, ",")
				for _, p := range peerList {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					slog.Info("Attempting to join Raft consensus cluster via peer", "peer", p)
					if err := cluster.JoinCluster(p, id, raftAddr); err == nil {
						slog.Info("Successfully joined Raft consensus cluster", "peer", p)
						break
					} else {
						slog.Warn("Failed to join Raft cluster via peer", "peer", p, "error", err)
					}
				}
			}()
		}
	}

	// Create S3 Gateway
	gateway := s3.NewGateway(store, authProvider, raftNode, clusterMgr, *replicationFactor, *erasureCoding, *dataShards, *parityShards)

	// Attach rate limiter if configured
	if *rateLimitRPS > 0 {
		burst := *rateLimitBurst
		if burst <= 0 {
			burst = (*rateLimitRPS) * 2
		}
		gateway.WithRateLimiter(ratelimit.NewLimiter(*rateLimitRPS, burst))
		slog.Info("Rate limiting enabled", "rps", *rateLimitRPS, "burst", burst)
	}

	if clusterMgr != nil && !*erasureCoding {
		healer := cluster.NewHealingManager(store, clusterMgr, *replicationFactor, *accessKey, *secretKey)
		healer.Start(context.Background(), 10*time.Second)
	}

	// Create Console Server (which wraps Gateway and serves Web UI)
	console := web.NewWebConsole(gateway, authProvider, store, clusterMgr, raftNode)

	// Start background lifecycle sweeper
	go func() {
		ticker := time.NewTicker(*lifecycleInterval)
		defer ticker.Stop()
		slog.Info("Lifecycle sweeper started", "interval", lifecycleInterval.String())
		for range ticker.C {
			expired, err := store.ApplyLifecycle(context.Background())
			if err != nil {
				slog.Error("Lifecycle sweep error", "error", err)
			} else if expired > 0 {
				slog.Info("Lifecycle sweep complete", "expired_versions", expired)
			}
		}
	}()

	addr := fmt.Sprintf(":%d", *port)

	// TLS mode: both cert and key must be provided together
	tlsEnabled := *tlsCert != "" && *tlsKey != ""
	if (*tlsCert == "") != (*tlsKey == "") {
		slog.Error("TLS configuration incomplete: both --tls-cert and --tls-key must be provided together")
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: console,
	}

	if tlsEnabled {
		// Validate cert/key pair is loadable before binding the port
		if _, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey); err != nil {
			slog.Error("Failed to load TLS certificate/key", "cert", *tlsCert, "key", *tlsKey, "error", err)
			os.Exit(1)
		}

		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS13,
			CurvePreferences: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
			},
		}
		srv.TLSConfig = tlsCfg
	}

	// Capture shutdown signals
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if tlsEnabled {
			slog.Info("TLS", "status", "enabled", "min_version", "TLS 1.3", "cert", *tlsCert)
			slog.Info("Console and S3 services starting (HTTPS)",
				"port", *port,
				"console_url", fmt.Sprintf("https://localhost:%d", *port),
				"s3_url", fmt.Sprintf("https://localhost:%d", *port),
			)
			if err := srv.ListenAndServeTLS(*tlsCert, *tlsKey); err != nil && err != http.ErrServerClosed {
				slog.Error("Server startup failed", "error", err)
				os.Exit(1)
			}
		} else {
			slog.Info("TLS", "status", "disabled", "note", "Pass --tls-cert and --tls-key to enable HTTPS/TLS 1.3")
			slog.Info("Console and S3 services starting (HTTP)",
				"port", *port,
				"console_url", fmt.Sprintf("http://localhost:%d", *port),
				"s3_url", fmt.Sprintf("http://localhost:%d", *port),
			)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("Server startup failed", "error", err)
				os.Exit(1)
			}
		}
	}()

	<-stopChan
	slog.Info("ServStore: Shutting down gracefully...")

	// 1. Shutdown HTTP server first
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("ServStore: HTTP server forced to shutdown", "error", err)
	}

	// 2. Shut down Raft Node
	if raftNode != nil {
		slog.Info("ServStore: Closing Raft node...")
		raftNode.Close()
	}

	// 3. Shut down Cluster Membership
	if clusterMgr != nil {
		slog.Info("ServStore: Closing cluster membership manager...")
		clusterMgr.Stop()
	}

	// 4. Close Storage Engine
	slog.Info("ServStore: Closing storage engine...")
	if err := store.Close(); err != nil {
		slog.Error("ServStore: Failed to close storage engine cleanly", "error", err)
	}

	slog.Info("ServStore: Shutdown complete")
}
