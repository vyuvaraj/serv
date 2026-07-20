package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vyuvaraj/ServShared"

	"servauth/pkg/handlers"
	"servauth/pkg/kms"
)

func main() {
	portStr := flag.String("port", "8098", "ServAuth server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	// Initialize RSA key pair for JWKS
	handlers.InitJWKS()

	// Initialize store
	handlers.InitStore()

	// SEC.8: Start background KMS envelope key rotation (simulated 24h rotation schedule)
	kms.StartKMSRotationLoop(24 * time.Hour)

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servauth", "1.0.0"))
	mux.HandleFunc("/api/v1/version", ServShared.VersionHandler("servauth", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/auth/register", handlers.HandleRegister)
	mux.HandleFunc("/api/auth/login", handlers.HandleLogin)
	mux.HandleFunc("/api/auth/jwks", handlers.HandleJWKS)
	mux.HandleFunc("/.well-known/jwks.json", handlers.HandleJWKS)
	mux.HandleFunc("/api/auth/rotate-keys", handlers.HandleRotateJWKS)
	mux.HandleFunc("/api/auth/reset-password/request", handlers.HandleResetRequest)
	mux.HandleFunc("/api/auth/reset-password/confirm", handlers.HandleResetConfirm)
	mux.HandleFunc("/oauth/token", handlers.HandleToken)
	mux.HandleFunc("/api/auth/keys", handlers.HandleKeys)
	mux.HandleFunc("/api/auth/keys/validate", handlers.HandleKeysValidate)
	mux.HandleFunc("/api/auth/keys/revoke", handlers.HandleKeysRevoke)
	mux.HandleFunc("/api/auth/sessions/revoke", handlers.HandleSessionsRevoke)
	mux.HandleFunc("/api/auth/mfa/setup", handlers.HandleMfaSetup)
	mux.HandleFunc("/api/auth/mfa/verify", handlers.HandleMfaVerify)
	mux.HandleFunc("/api/auth/social/login", handlers.HandleSocialLogin)
	mux.HandleFunc("/api/auth/social/callback", handlers.HandleSocialCallback)
	mux.HandleFunc("/api/auth/users", handlers.HandleUsers)
	mux.HandleFunc("/api/auth/users/roles", handlers.HandleUsersRoles)
	mux.HandleFunc("/api/auth/sessions", handlers.HandleSessions)
	mux.HandleFunc("/api/auth/secrets/encrypt", handlers.HandleSecretsEncrypt)
	mux.HandleFunc("/api/auth/secrets/decrypt", handlers.HandleSecretsDecrypt)
	mux.HandleFunc("/api/auth/risk", handlers.HandleAdaptiveRiskScore)
	mux.HandleFunc("/api/auth/security/stuffing-detector", handlers.HandleCredentialStuffing)
	mux.HandleFunc("/api/auth/magic-link/request", handlers.HandleMagicLinkRequest)
	mux.HandleFunc("/api/auth/magic-link/verify", handlers.HandleMagicLinkVerify)
	mux.HandleFunc("/api/auth/passkey/register/challenge", handlers.HandlePasskeyRegisterChallenge)
	mux.HandleFunc("/api/auth/passkey/register/verify", handlers.HandlePasskeyRegisterVerify)
	mux.HandleFunc("/api/auth/passkey/login/challenge", handlers.HandlePasskeyLoginChallenge)
	mux.HandleFunc("/api/auth/passkey/login/verify", handlers.HandlePasskeyLoginVerify)
	mux.HandleFunc("/scim/v2/Users", handlers.HandleSCIMUsers)
	mux.HandleFunc("/scim/v2/Users/", handlers.HandleSCIMUsers)

	// Wrapper handler for /api/v1/ prefix rewriting (V1.1 support)
	v1Wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/oauth/") {
			r.URL.Path = "/oauth/" + strings.TrimPrefix(r.URL.Path, "/api/v1/oauth/")
		} else if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
		}
		mux.ServeHTTP(w, r)
	})

	// Wrap in ServShared middleware: OTel tracing → RateLimit → CORS → MaxBytes → JWT auth → tenant enforcement → handlers
	serverHandler := ServShared.TraceMiddleware("servauth",
		ServShared.RateLimitMiddleware(
			ServShared.CORSMiddleware(
				ServShared.MaxBytesMiddleware(10*1024*1024)(
					ServShared.AuthMiddleware(
						handlers.RevocationMiddleware(
							ServShared.TenantMiddleware(v1Wrapper),
						),
					),
				),
			),
		),
	)

	// Setup Server
	server := &http.Server{
		Addr:    ":" + port,
		Handler: serverHandler,
	}

	// Channel to catch OS signals for Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start server in background
	go func() {
		log.Printf("[INFO] ServAuth server starting on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start ServAuth: %v", err)
		}
	}()

	// Wait for SIGTERM/SIGINT
	<-stop

	log.Println("[INFO] Shutting down ServAuth server...")

	// Shutdown OTel
	ServShared.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
	log.Println("[INFO] ServAuth server exited cleanly")
}
