package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vyuvaraj/ServShared"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	Username       string    `json:"username"`
	Email          string    `json:"email"`
	Password       string    `json:"-"`
	Salt           string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	FailedAttempts int       `json:"-"`
	LockedUntil    time.Time `json:"-"`
	ResetToken     string    `json:"-"`
	TenantID       string    `json:"tenant_id,omitempty"`
	MFASecret      string    `json:"-"`
	MFAEnabled     bool      `json:"mfa_enabled"`
}

type ResetRequest struct {
	Email string `json:"email"`
}

type ResetConfirm struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

type APIKey struct {
	Key       string    `json:"key"`
	Username  string    `json:"username"`
	Scopes    []string  `json:"scopes"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
}

var (
	users      = make(map[string]User) // key: username
	usersMu    sync.RWMutex
	apiKeys    = make(map[string]*APIKey) // key: token/key
	apiKeysMu  sync.RWMutex
	sessions   = make(map[string]*Session) // key: token
	sessionsMu sync.RWMutex
	clients    = map[string]string{
		"console-client-id": "console-secret-key-9876",
	}
)

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
func isSessionExpired(s *Session) bool {
	return time.Since(s.CreatedAt) > 24*time.Hour
}

// getKMSKey retrieves or defaults the key for AES-GCM encryption
func getKMSKey() []byte {
	secret := os.Getenv("SERV_KMS_SECRET")
	if secret == "" {
		secret = "default-kms-secret-32-bytes-long!"
	}
	key := make([]byte, 32)
	copy(key, []byte(secret))
	return key
}

// encryptAES encrypts plaintext using AES-GCM
func encryptAES(plaintext string) (string, error) {
	key := getKMSKey()
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
	return hex.EncodeToString(ciphertext), nil
}

// decryptAES decrypts ciphertext using AES-GCM
func decryptAES(hexCiphertext string) (string, error) {
	key := getKMSKey()
	ciphertext, err := hex.DecodeString(hexCiphertext)
	if err != nil {
		return "", err
	}
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

// verifyTOTP validates an RFC 6238 time-based one-time password
func verifyTOTP(secret string, code string) bool {
	var expectedCode int
	if _, err := fmt.Sscanf(code, "%d", &expectedCode); err != nil {
		return false
	}

	currentTime := time.Now().Unix()
	step := int64(30)
	key := []byte(secret)

	// Allow 1 step window for clock drift
	for i := -1; i <= 1; i++ {
		counter := (currentTime / step) + int64(i)
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(counter))

		mac := hmac.New(sha1.New, key)
		mac.Write(buf)
		hs := mac.Sum(nil)

		offset := hs[len(hs)-1] & 0x0f
		binCode := int(hs[offset]&0x7f)<<24 |
			int(hs[offset+1]&0xff)<<16 |
			int(hs[offset+2]&0xff)<<8 |
			int(hs[offset+3]&0xff)

		otp := binCode % 1000000
		if otp == expectedCode {
			return true
		}
	}
	return false
}

var userStore UserStore

func initStore() {
	client := ServShared.NewStoreClient()
	userStore = NewServStoreUserStore(client)
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
	copied := make(map[string]User)
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
	copied := make(map[string]*APIKey)
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
	copied := make(map[string]*Session)
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

	initStore()

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/auth/register", handleRegister)
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/reset-password/request", handleResetRequest)
	mux.HandleFunc("/api/auth/reset-password/confirm", handleResetConfirm)
	mux.HandleFunc("/oauth/token", handleToken)
	mux.HandleFunc("/api/auth/keys", handleKeys)
	mux.HandleFunc("/api/auth/keys/validate", handleKeysValidate)
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

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" || req.Email == "" {
		http.Error(w, "Username, email, and password are required", http.StatusBadRequest)
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

	newUser := User{
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newUser)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload: "+err.Error(), http.StatusBadRequest)
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

	token, err := ServShared.GenerateUserToken(secret, user.Username, []string{"user"}, tenantID, 24*time.Hour)
	if err != nil {
		http.Error(w, "Token generation failed", http.StatusInternalServerError)
		return
	}

	sessionsMu.Lock()
	sessions[token] = &Session{
		Token:     token,
		Username:  user.Username,
		IP:        r.RemoteAddr,
		UserAgent: r.UserAgent(),
		CreatedAt: time.Now(),
		Revoked:   false,
	}
	sessionsMu.Unlock()
	saveSessionsToStore()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(LoginResponse{
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

	expectedSecret, ok := clients[clientID]
	if !ok || expectedSecret != clientSecret {
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
	tokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token, err := tokenObj.SignedString([]byte(secret))
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

	var req ResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
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

	var req ResetConfirm
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
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

	apiKey := &APIKey{
		Key:       hexKey,
		Username:  req.Username,
		Scopes:    req.Scopes,
		CreatedAt: time.Now(),
	}

	apiKeysMu.Lock()
	apiKeys[hexKey] = apiKey
	apiKeysMu.Unlock()
	saveAPIKeysToStore()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":      hexKey,
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

	apiKeysMu.RLock()
	apiKey, exists := apiKeys[req.Key]
	apiKeysMu.RUnlock()

	if !exists {
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(apiKey)
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

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Session revoked successfully"}`))
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
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	mockSecret := "secret-totp-key-for-" + req.Username
	user.MFASecret = mockSecret
	users[userKey] = user
	usersMu.Unlock()
	saveUsersToStore()

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
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Verify the real TOTP code
	if verifyTOTP(user.MFASecret, req.Code) {
		user.MFAEnabled = true
		users[userKey] = user
		usersMu.Unlock()
		saveUsersToStore()

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
		users[userKey] = User{
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

	token, err := ServShared.GenerateUserToken(secret, username, []string{"user"}, tenantID, 24*time.Hour)
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
		http.Error(w, "User not found", http.StatusNotFound)
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
	var list []*Session
	for _, s := range sessions {
		if !s.Revoked && !isSessionExpired(s) {
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"plaintext": plaintext,
	})
}
