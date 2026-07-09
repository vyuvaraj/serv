package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vyuvaraj/ServShared"
	"golang.org/x/crypto/bcrypt"

	"servauth/pkg/mfa"
	"servauth/pkg/oauth"
	"servauth/pkg/store"
)

var (
	users      = make(map[string]store.User) // key: username
	usersMu    sync.RWMutex
	apiKeys    = make(map[string]*store.APIKey) // key: token/key
	apiKeysMu  sync.RWMutex
	sessions   = make(map[string]*store.Session) // key: token
	sessionsMu sync.RWMutex
	clients    = map[string]string{
		"console-client-id": "console-secret-key-9876",
	}

	EnterpriseRegisterAuthSession = func(token string, username string, ip string, userAgent string) error { return nil }
	EnterpriseVerifyAuthSession   = func(token string) bool { return true }
	EnterpriseRevokeAuthSession   = func(token string) error { return nil }

	// AI.32 stuffing detection variables
	failedLoginsIP   = make(map[string][]time.Time)
	failedLoginsIPMu sync.Mutex


	// JWKS Keys
	jwtRSAPrivateKey *rsa.PrivateKey
	jwtRSAPublicKey  *rsa.PublicKey
	jwtKeyID         = "servverse-key-v1"

	jwkKeyPairs   []JWKKeyPair
	jwkKeyPairsMu sync.RWMutex
)

type JWKKeyPair struct {
	KeyID      string
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
	CreatedAt  time.Time
}

// hashPassword hashes password using bcrypt
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// verifyPassword checks password against bcrypt hash
func verifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// isSessionExpired checks if a session has expired (TTL of 24 hours)
func isSessionExpired(s *store.Session) bool {
	return time.Since(s.CreatedAt) > 24*time.Hour
}

var (
	kmsKeys = map[string]string{
		"v1": "default-kms-secret-32-bytes-long!",
		"v2": "rotated-kms-secret-32-bytes-long!",
	}
	latestKMSKeyVersion = "v2"
	kmsMu               sync.RWMutex
)

func startKMSRotationLoop(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		versionCounter := 2
		for range ticker.C {
			kmsMu.Lock()
			versionCounter++
			newVersion := fmt.Sprintf("v%d", versionCounter)

			// Generate a new 32-byte key dynamically
			newKeyBytes := make([]byte, 32)
			_, _ = io.ReadFull(rand.Reader, newKeyBytes)
			newKeyHex := hex.EncodeToString(newKeyBytes)

			kmsKeys[newVersion] = newKeyHex
			latestKMSKeyVersion = newVersion
			kmsMu.Unlock()
			log.Printf("[INFO] Rotated KMS envelope key to version %s", newVersion)
		}
	}()
}

func getKMSKeyForVersion(version string) []byte {
	envKey := "SERV_KMS_SECRET_" + strings.ToUpper(version)
	secret := os.Getenv(envKey)
	if secret == "" {
		kmsMu.RLock()
		val, ok := kmsKeys[version]
		kmsMu.RUnlock()
		if ok {
			secret = val
		} else {
			secret = os.Getenv("SERV_KMS_SECRET")
			if secret == "" {
				secret = "default-kms-secret-32-bytes-long!"
			}
		}
	}
	key := make([]byte, 32)
	copy(key, []byte(secret))
	return key
}

// encryptAES encrypts plaintext using AES-GCM with versioning
func encryptAES(plaintext string) (string, error) {
	kmsMu.RLock()
	version := os.Getenv("SERV_KMS_SECRET_LATEST_VERSION")
	if version == "" {
		version = latestKMSKeyVersion
	}
	kmsMu.RUnlock()
	key := getKMSKeyForVersion(version)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return version + ":" + hex.EncodeToString(ciphertext), nil
}

// decryptAES decrypts versioned ciphertext using AES-GCM, with multi-version fallback.
func decryptAES(prefixedCiphertext string) (string, error) {
	parts := strings.SplitN(prefixedCiphertext, ":", 2)
	var version string
	var hexCiphertext string
	if len(parts) == 2 {
		version = parts[0]
		hexCiphertext = parts[1]
	} else {
		// Legacy unversioned format fallback
		version = "v1"
		hexCiphertext = prefixedCiphertext
	}

	ciphertext, err := hex.DecodeString(hexCiphertext)
	if err != nil {
		return "", err
	}

	key := getKMSKeyForVersion(version)
	plaintext, err := decryptWithKey(ciphertext, key)
	if err == nil {
		return plaintext, nil
	}

	kmsMu.RLock()
	activeVersions := make([]string, 0, len(kmsKeys))
	for k := range kmsKeys {
		if k != version {
			activeVersions = append(activeVersions, k)
		}
	}
	kmsMu.RUnlock()

	for _, v := range activeVersions {
		key = getKMSKeyForVersion(v)
		plaintext, err = decryptWithKey(ciphertext, key)
		if err == nil {
			log.Printf("[INFO] Decrypted ciphertext with fallback KMS key version %s", v)
			return plaintext, nil
		}
	}

	return "", fmt.Errorf("decryption failed for all active KMS versions")
}

func decryptWithKey(ciphertext []byte, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

var userStore store.UserStore

func initStore() {
	client := ServShared.NewStoreClient()
	userStore = store.NewServStoreUserStore(client)
	loadStateFromStore()
}

func loadStateFromStore() {
	if u, err := userStore.LoadUsers(); err == nil {
		usersMu.Lock()
		users = u
		usersMu.Unlock()
	}
	if k, err := userStore.LoadKeys(); err == nil {
		apiKeysMu.Lock()
		apiKeys = k
		apiKeysMu.Unlock()
	}
	if s, err := userStore.LoadSessions(); err == nil {
		sessionsMu.Lock()
		sessions = s
		sessionsMu.Unlock()
	}
}

func saveUsersToStore() {
	if userStore == nil {
		return
	}
	usersMu.RLock()
	copied := make(map[string]store.User)
	for k, v := range users {
		copied[k] = v
	}
	usersMu.RUnlock()
	_ = userStore.SaveUsers(copied)
}

func saveAPIKeysToStore() {
	if userStore == nil {
		return
	}
	apiKeysMu.RLock()
	copied := make(map[string]*store.APIKey)
	for k, v := range apiKeys {
		copied[k] = v
	}
	apiKeysMu.RUnlock()
	_ = userStore.SaveKeys(copied)
}

func saveSessionsToStore() {
	if userStore == nil {
		return
	}
	sessionsMu.RLock()
	copied := make(map[string]*store.Session)
	for k, v := range sessions {
		copied[k] = v
	}
	sessionsMu.RUnlock()
	_ = userStore.SaveSessions(copied)
}

func main() {
	portStr := flag.String("port", "8098", "ServAuth server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	// Initialize RSA key pair for JWKS
	initJWKS()

	initStore()

	// SEC.8: Start background KMS envelope key rotation (simulated 24h rotation schedule)
	startKMSRotationLoop(24 * time.Hour)

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servauth", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/auth/register", handleRegister)
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/jwks", handleJWKS)
	mux.HandleFunc("/.well-known/jwks.json", handleJWKS)
	mux.HandleFunc("/api/auth/rotate-keys", handleRotateJWKS)
	mux.HandleFunc("/api/auth/reset-password/request", handleResetRequest)
	mux.HandleFunc("/api/auth/reset-password/confirm", handleResetConfirm)
	mux.HandleFunc("/oauth/token", handleToken)
	mux.HandleFunc("/api/auth/keys", handleKeys)
	mux.HandleFunc("/api/auth/keys/validate", handleKeysValidate)
	mux.HandleFunc("/api/auth/keys/revoke", handleKeysRevoke)
	mux.HandleFunc("/api/auth/sessions/revoke", handleSessionsRevoke)
	mux.HandleFunc("/api/auth/mfa/setup", handleMfaSetup)
	mux.HandleFunc("/api/auth/mfa/verify", handleMfaVerify)
	mux.HandleFunc("/api/auth/social/login", handleSocialLogin)
	mux.HandleFunc("/api/auth/social/callback", handleSocialCallback)
	mux.HandleFunc("/api/auth/users", handleUsers)
	mux.HandleFunc("/api/auth/users/roles", handleUsersRoles)
	mux.HandleFunc("/api/auth/sessions", handleSessions)
	mux.HandleFunc("/api/auth/secrets/encrypt", handleSecretsEncrypt)
	mux.HandleFunc("/api/auth/secrets/decrypt", handleSecretsDecrypt)
	mux.HandleFunc("/api/auth/risk", handleAdaptiveRiskScore)
	mux.HandleFunc("/api/auth/security/stuffing-detector", handleCredentialStuffing)


	// Wrap in ServShared middleware: OTel tracing → JWT auth → tenant enforcement → handlers
	serverHandler := ServShared.TraceMiddleware("servauth",
		ServShared.AuthMiddleware(
			ServShared.TenantMiddleware(mux),
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

func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req store.RegisterRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	userKey := tenantID + ":" + req.Username

	usersMu.Lock()
	if _, exists := users[userKey]; exists {
		usersMu.Unlock()
		http.Error(w, "Username already exists in this tenant", http.StatusConflict)
		return
	}

	saltBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, saltBytes); err != nil {
		usersMu.Unlock()
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	salt := hex.EncodeToString(saltBytes)

	hashedPassword, err := hashPassword(req.Password)
	if err != nil {
		usersMu.Unlock()
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	newUser := store.User{
		Username:  req.Username,
		Email:     req.Email,
		Password:  hashedPassword,
		Salt:      salt,
		CreatedAt: time.Now(),
		TenantID:  tenantID,
	}

	users[userKey] = newUser
	usersMu.Unlock()

	saveUsersToStore()
	_ = ServShared.EmitAuditEvent("ServAuth", "USER_REGISTER", req.Username, map[string]interface{}{"email": req.Email, "tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newUser)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req store.LoginRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	userKey := tenantID + ":" + req.Username

	usersMu.Lock()
	user, exists := users[userKey]
	if exists && !user.LockedUntil.IsZero() && user.LockedUntil.After(time.Now()) {
		usersMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"lockout","message":"Account is locked due to multiple failed login attempts."}`))
		return
	}
	usersMu.Unlock()

	if !exists {
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	if !verifyPassword(req.Password, user.Password) {
		// AI.32 track stuffing IP
		ip := r.RemoteAddr
		if idx := strings.Index(ip, ":"); idx > 0 {
			ip = ip[:idx]
		}
		recordFailedLogin(ip)

		usersMu.Lock()
		u := users[userKey]
		u.FailedAttempts++
		if u.FailedAttempts >= 3 {
			u.LockedUntil = time.Now().Add(5 * time.Minute)
		}
		users[userKey] = u
		usersMu.Unlock()
		saveUsersToStore()

		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	// Reset attempts on success
	usersMu.Lock()
	u := users[userKey]
	u.FailedAttempts = 0
	u.LockedUntil = time.Time{}
	users[userKey] = u
	usersMu.Unlock()
	saveUsersToStore()

	// Generate JWT using ServShared Secret or default test key
	secret := os.Getenv("SERV_JWT_SECRET")
	if secret == "" {
		secret = "test-secret-key-12345"
	}

	var token string
	var err error
	if os.Getenv("SERV_JWKS_URL") != "" || os.Getenv("SERV_JWT_SIGNING_METHOD") == "RS256" {
		token, err = ServShared.GenerateUserTokenRS256(jwtRSAPrivateKey, jwtKeyID, user.Username, []string{"user"}, tenantID, 24*time.Hour)
	} else {
		token, err = ServShared.GenerateUserToken(secret, user.Username, []string{"user"}, tenantID, 24*time.Hour)
	}
	if err != nil {
		http.Error(w, "Token generation failed", http.StatusInternalServerError)
		return
	}

	sessionsMu.Lock()
	sessions[token] = &store.Session{
		Token:     token,
		Username:  user.Username,
		IP:        r.RemoteAddr,
		UserAgent: r.UserAgent(),
		CreatedAt: time.Now(),
		Revoked:   false,
	}
	sessionsMu.Unlock()
	saveSessionsToStore()
	_ = EnterpriseRegisterAuthSession(token, user.Username, r.RemoteAddr, r.UserAgent())
	_ = ServShared.EmitAuditEvent("ServAuth", "USER_LOGIN", user.Username, map[string]interface{}{"ip": r.RemoteAddr, "tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(store.LoginResponse{
		Token:    token,
		Username: user.Username,
	})
}

func handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var clientID, clientSecret, grantType string
	if r.Header.Get("Content-Type") == "application/json" {
		var req struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			GrantType    string `json:"grant_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			clientID = req.ClientID
			clientSecret = req.ClientSecret
			grantType = req.GrantType
		}
	} else {
		_ = r.ParseForm()
		clientID = r.FormValue("client_id")
		clientSecret = r.FormValue("client_secret")
		grantType = r.FormValue("grant_type")
	}

	if clientID == "" {
		username, password, ok := r.BasicAuth()
		if ok {
			clientID = username
			clientSecret = password
		}
	}

	if grantType != "client_credentials" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"unsupported_grant_type"}`))
		return
	}

	if !oauth.ValidateClient(clientID, clientSecret, clients) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_client"}`))
		return
	}

	secret := os.Getenv("SERV_JWT_SECRET")
	if secret == "" {
		secret = "test-secret-key-12345"
	}

	claims := ServShared.Claims{
		Username: clientID,
		Roles:    []string{"client"},
		Scopes:   []string{"*"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "servauth",
			Subject:   clientID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	var token string
	var err error
	if os.Getenv("SERV_JWKS_URL") != "" || os.Getenv("SERV_JWT_SIGNING_METHOD") == "RS256" {
		tokenObj := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenObj.Header["kid"] = jwtKeyID
		token, err = tokenObj.SignedString(jwtRSAPrivateKey)
	} else {
		tokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		token, err = tokenObj.SignedString([]byte(secret))
	}
	if err != nil {
		http.Error(w, "Token generation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"access_token":"%s","token_type":"Bearer","expires_in":3600}`, token)))
}

func handleResetRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req store.ResetRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	usersMu.Lock()
	found := false
	var username string
	for name, user := range users {
		if user.Email == req.Email {
			found = true
			username = name
			break
		}
	}

	if !found {
		usersMu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"Reset link sent if email exists"}`))
		return
	}

	token := fmt.Sprintf("rst-%d", time.Now().UnixNano())
	u := users[username]
	u.ResetToken = token
	users[username] = u
	usersMu.Unlock()
	saveUsersToStore()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "token": token})
}

func handleResetConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req store.ResetConfirm
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	usersMu.Lock()
	found := false
	var username string
	for name, user := range users {
		if user.ResetToken != "" && user.ResetToken == req.Token {
			found = true
			username = name
			break
		}
	}

	if !found {
		usersMu.Unlock()
		http.Error(w, "Invalid or expired token", http.StatusBadRequest)
		return
	}

	u := users[username]
	hashed, err := hashPassword(req.Password)
	if err != nil {
		usersMu.Unlock()
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	u.Password = hashed
	u.ResetToken = "" // invalidate token
	u.FailedAttempts = 0
	u.LockedUntil = time.Time{}
	users[username] = u
	usersMu.Unlock()
	saveUsersToStore()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Password updated successfully"}`))
}

func handleKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Generate key
	rawKey := fmt.Sprintf("key-%d-%s", time.Now().UnixNano(), req.Username)
	keyHash := sha256.Sum256([]byte(rawKey))
	hexKey := hex.EncodeToString(keyHash[:])

	// SEC.8: Hash the API Key before storing it
	hashedBytes := sha256.Sum256([]byte(hexKey))
	hashedKey := hex.EncodeToString(hashedBytes[:])

	apiKey := &store.APIKey{
		Key:       hashedKey,
		Username:  req.Username,
		Scopes:    req.Scopes,
		CreatedAt: time.Now(),
	}

	apiKeysMu.Lock()
	apiKeys[hashedKey] = apiKey
	apiKeysMu.Unlock()
	saveAPIKeysToStore()

	_ = ServShared.EmitAuditEvent("ServAuth", "API_KEY_ISSUE", req.Username, map[string]interface{}{"scopes": req.Scopes})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":      hexKey, // Return plaintext key only once to the registering client
		"username": req.Username,
		"scopes":   req.Scopes,
	})
}

func handleKeysValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// SEC.8: Hash the incoming validate request key before looking it up
	hashedBytes := sha256.Sum256([]byte(req.Key))
	hashedKey := hex.EncodeToString(hashedBytes[:])

	apiKeysMu.RLock()
	apiKey, exists := apiKeys[hashedKey]
	apiKeysMu.RUnlock()

	if !exists || apiKey.Revoked {
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return
	}

	_ = ServShared.EmitAuditEvent("ServAuth", "API_KEY_VALIDATE", apiKey.Username, map[string]interface{}{"scopes": apiKey.Scopes})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(apiKey)
}

func handleKeysRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	hashedBytes := sha256.Sum256([]byte(req.Key))
	hashedKey := hex.EncodeToString(hashedBytes[:])

	apiKeysMu.Lock()
	apiKey, exists := apiKeys[hashedKey]
	if exists {
		apiKey.Revoked = true
	}
	apiKeysMu.Unlock()

	if !exists {
		http.Error(w, "API key not found", http.StatusNotFound)
		return
	}

	saveAPIKeysToStore()
	_ = ServShared.EmitAuditEvent("ServAuth", "API_KEY_REVOKE", apiKey.Username, map[string]interface{}{"scopes": apiKey.Scopes})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"API key revoked"}`))
}


func handleSessionsRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	sessionsMu.Lock()
	session, exists := sessions[req.Token]
	if exists {
		if isSessionExpired(session) {
			exists = false
		} else {
			session.Revoked = true
		}
	}
	sessionsMu.Unlock()
	saveSessionsToStore()
	_ = EnterpriseRevokeAuthSession(req.Token)

	if !exists {
		http.Error(w, "store.Session not found", http.StatusNotFound)
		return
	}

	_ = ServShared.EmitAuditEvent("ServAuth", "SESSION_REVOKE", session.Username, map[string]interface{}{"token": req.Token})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"store.Session revoked successfully"}`))
}

func handleMfaSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	userKey := tenantID + ":" + req.Username

	usersMu.Lock()
	user, exists := users[userKey]
	if !exists {
		usersMu.Unlock()
		http.Error(w, "store.User not found", http.StatusNotFound)
		return
	}

	mockSecret := "secret-totp-key-for-" + req.Username
	user.MFASecret = mockSecret
	users[userKey] = user
	usersMu.Unlock()
	saveUsersToStore()

	_ = ServShared.EmitAuditEvent("ServAuth", "MFA_SETUP", req.Username, map[string]interface{}{"tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"secret":     mockSecret,
		"issuer":     "Servverse",
		"account":    req.Username,
		"qr_mock_url": "https://api.qrserver.com/v1/create-qr-code/?data=otpauth://totp/Servverse:" + req.Username,
	})
}

func handleMfaVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	userKey := tenantID + ":" + req.Username

	usersMu.Lock()
	user, exists := users[userKey]
	if !exists {
		usersMu.Unlock()
		http.Error(w, "store.User not found", http.StatusNotFound)
		return
	}

	// Verify the real TOTP code
	if mfa.VerifyTOTP(user.MFASecret, req.Code) {
		user.MFAEnabled = true
		users[userKey] = user
		usersMu.Unlock()
		saveUsersToStore()

		_ = ServShared.EmitAuditEvent("ServAuth", "MFA_VERIFY", req.Username, map[string]interface{}{"tenant": tenantID})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"MFA verified and enabled successfully"}`))
		return
	}

	usersMu.Unlock()
	http.Error(w, "Invalid verification code", http.StatusUnauthorized)
}

func handleSocialLogin(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider != "google" && provider != "github" {
		http.Error(w, "Unsupported provider", http.StatusBadRequest)
		return
	}

	redirectURL := fmt.Sprintf("https://auth.provider.com/%s/authorize?client_id=mock-client&redirect_uri=mock-redirect&response_type=code", provider)
	_ = ServShared.EmitAuditEvent("ServAuth", "SOCIAL_LOGIN_REDIRECT", "guest", map[string]interface{}{"provider": provider})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":       "redirect_simulated",
		"redirect_url": redirectURL,
	})
}

func handleSocialCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Code == "" || len(req.Code) < 4 {
		http.Error(w, "Invalid auth code", http.StatusBadRequest)
		return
	}

	username := fmt.Sprintf("social-%s-%s", req.Provider, req.Code[:4])
	userKey := username

	usersMu.Lock()
	_, exists := users[userKey]
	if !exists {
		users[userKey] = store.User{
			Username: username,
			Salt:     "salt-social",
			Password: "hash-social",
		}
		usersMu.Unlock()
		saveUsersToStore()
	} else {
		usersMu.Unlock()
	}

	secret := os.Getenv("SERV_JWT_SECRET")
	if secret == "" {
		secret = "test-secret-key-12345"
	}

	tenantID := ServShared.GetTenantID(r)
	if tenantID == "" {
		tenantID = "default"
	}

	var token string
	var err error
	if os.Getenv("SERV_JWKS_URL") != "" || os.Getenv("SERV_JWT_SIGNING_METHOD") == "RS256" {
		token, err = ServShared.GenerateUserTokenRS256(jwtRSAPrivateKey, jwtKeyID, username, []string{"user"}, tenantID, 24*time.Hour)
	} else {
		token, err = ServShared.GenerateUserToken(secret, username, []string{"user"}, tenantID, 24*time.Hour)
	}
	if err != nil {
		http.Error(w, "Token generation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":       "success",
		"username":     username,
		"access_token": token,
	})
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	usersMu.RLock()
	var list []map[string]interface{}
	for _, u := range users {
		if u.TenantID == tenantID {
			list = append(list, map[string]interface{}{
				"username":    u.Username,
				"email":       u.Email,
				"tenant_id":   u.TenantID,
				"mfa_enabled": u.MFAEnabled,
			})
		}
	}
	usersMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(list)
}

func handleUsersRoles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	userKey := tenantID + ":" + req.Username

	usersMu.Lock()
	user, exists := users[userKey]
	usersMu.Unlock()

	if !exists {
		http.Error(w, "store.User not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"username": user.Username,
		"scopes":   req.Scopes,
	})
}

func handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionsMu.RLock()
	var list []*store.Session
	for _, s := range sessions {
		if !s.Revoked && !isSessionExpired(s) && EnterpriseVerifyAuthSession(s.Token) {
			list = append(list, s)
		}
	}
	sessionsMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(list)
}

func handleSecretsEncrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	ciphertext, err := encryptAES(req.Plaintext)
	if err != nil {
		http.Error(w, "Encryption failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	_ = ServShared.EmitAuditEvent("ServAuth", "SECRET_ENCRYPT", "system", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"ciphertext":         ciphertext,
		"encrypted_data_key": "mock-datakey-9876",
	})
}

func handleSecretsDecrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Ciphertext       string `json:"ciphertext"`
		EncryptedDataKey string `json:"encrypted_data_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	plaintext, err := decryptAES(req.Ciphertext)
	if err != nil {
		http.Error(w, "Decryption failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	_ = ServShared.EmitAuditEvent("ServAuth", "SECRET_DECRYPT", "system", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"plaintext": plaintext,
	})
}

func handleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	jwkKeyPairsMu.RLock()
	defer jwkKeyPairsMu.RUnlock()

	var keys []map[string]interface{}
	for _, pair := range jwkKeyPairs {
		nStr := base64.RawURLEncoding.EncodeToString(pair.PublicKey.N.Bytes())
		eBytes := big.NewInt(int64(pair.PublicKey.E)).Bytes()
		eStr := base64.RawURLEncoding.EncodeToString(eBytes)

		keys = append(keys, map[string]interface{}{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": pair.KeyID,
			"n":   nStr,
			"e":   eStr,
		})
	}

	jwks := map[string]interface{}{
		"keys": keys,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(jwks)
}

func initJWKS() {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("failed to generate RSA key pair: %v", err)
	}
	pub := &priv.PublicKey
	kid := fmt.Sprintf("servverse-key-%d", time.Now().UnixNano())

	jwkKeyPairsMu.Lock()
	jwkKeyPairs = append(jwkKeyPairs, JWKKeyPair{
		KeyID:      kid,
		PrivateKey: priv,
		PublicKey:  pub,
		CreatedAt:  time.Now(),
	})
	jwtRSAPrivateKey = priv
	jwtRSAPublicKey = pub
	jwtKeyID = kid
	jwkKeyPairsMu.Unlock()
}

func handleRotateJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		http.Error(w, "Key generation failed", http.StatusInternalServerError)
		return
	}
	pub := &priv.PublicKey
	kid := fmt.Sprintf("servverse-key-%d", time.Now().UnixNano())

	jwkKeyPairsMu.Lock()
	jwkKeyPairs = append(jwkKeyPairs, JWKKeyPair{
		KeyID:      kid,
		PrivateKey: priv,
		PublicKey:  pub,
		CreatedAt:  time.Now(),
	})
	jwtRSAPrivateKey = priv
	jwtRSAPublicKey = pub
	jwtKeyID = kid
	jwkKeyPairsMu.Unlock()

	_ = ServShared.EmitAuditEvent("ServAuth", "JWKS_ROTATE", "system", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"status":"success","rotated_to":"%s"}`, kid)))
}

func recordFailedLogin(ip string) {
	failedLoginsIPMu.Lock()
	defer failedLoginsIPMu.Unlock()
	failedLoginsIP[ip] = append(failedLoginsIP[ip], time.Now())
}

func handleAdaptiveRiskScore(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	// Default mock risk scoring algorithm checking typical indicators
	ua := r.Header.Get("User-Agent")
	ip := r.RemoteAddr
	timeHour := time.Now().Hour()

	risk := 0.1
	// Logins during unusual hours (e.g. 1 AM to 5 AM) increase risk
	if timeHour >= 1 && timeHour <= 5 {
		risk += 0.3
	}
	// Missing User-Agent increases risk
	if ua == "" {
		risk += 0.2
	}
	// Localhost flag risk reduction
	if strings.HasPrefix(ip, "127.0.0.1") || strings.HasPrefix(ip, "[::1]") {
		risk = 0.05
	}

	if risk > 1.0 {
		risk = 1.0
	}

	requireMFA := risk >= 0.5
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"risk_score":%.2f,"require_mfa":%t}`, risk, requireMFA)))
}

func handleCredentialStuffing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	failedLoginsIPMu.Lock()
	defer failedLoginsIPMu.Unlock()

	stuffingIPs := []string{}
	now := time.Now()

	// Detect if any IP has had more than 3 failures in the last 60 seconds
	for ip, attempts := range failedLoginsIP {
		recent := 0
		for _, t := range attempts {
			if now.Sub(t) < 60*time.Second {
				recent++
			}
		}
		if recent >= 3 {
			stuffingIPs = append(stuffingIPs, ip)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stuffing_detected": len(stuffingIPs) > 0,
		"flagged_ips":        stuffingIPs,
	})
}


