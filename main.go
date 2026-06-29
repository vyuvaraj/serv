package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
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

// hashPassword hashes password with sha256 and salt
func hashPassword(password, salt string) string {
	hasher := sha256.New()
	hasher.Write([]byte(password + salt))
	return hex.EncodeToString(hasher.Sum(nil))
}

func main() {
	portStr := flag.String("port", "8098", "ServAuth server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

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

	// Wrap in ServShared middleware (auth checks for dashboard endpoints if needed, but signup/login are public)
	serverHandler := ServShared.AuthMiddleware(mux)

	log.Printf("ServAuth server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, serverHandler); err != nil {
		log.Fatalf("failed to start ServAuth: %v", err)
	}
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
	defer usersMu.Unlock()

	if _, exists := users[userKey]; exists {
		http.Error(w, "Username already exists in this tenant", http.StatusConflict)
		return
	}

	salt := fmt.Sprintf("%d", time.Now().UnixNano())
	hashedPassword := hashPassword(req.Password, salt)

	newUser := User{
		Username:  req.Username,
		Email:     req.Email,
		Password:  hashedPassword,
		Salt:      salt,
		CreatedAt: time.Now(),
		TenantID:  tenantID,
	}

	users[userKey] = newUser

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

	hashed := hashPassword(req.Password, user.Salt)
	if hashed != user.Password {
		usersMu.Lock()
		u := users[userKey]
		u.FailedAttempts++
		if u.FailedAttempts >= 3 {
			u.LockedUntil = time.Now().Add(5 * time.Minute)
		}
		users[userKey] = u
		usersMu.Unlock()

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

	// Generate JWT using ServShared Secret or default test key
	secret := os.Getenv("SERV_JWT_SECRET")
	if secret == "" {
		secret = "test-secret-key-12345"
	}

	// Simple JWT generation payload
	claims := map[string]interface{}{
		"sub":  user.Username,
		"email": user.Email,
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
	}
	claimsBytes, _ := json.Marshal(claims)
	token := base64Encode(claimsBytes) // Simple representation

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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(LoginResponse{
		Token:    token,
		Username: user.Username,
	})
}

func base64Encode(src []byte) string {
	// Custom simple mock token for dev mode compatibility
	hasher := sha256.New()
	hasher.Write(src)
	return hex.EncodeToString(hasher.Sum(nil))
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

	claims := map[string]interface{}{
		"sub": clientID,
		"iss": "servauth",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	claimsBytes, _ := json.Marshal(claims)
	token := base64Encode(claimsBytes)

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
	defer usersMu.Unlock()

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
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"Reset link sent if email exists"}`))
		return
	}

	token := fmt.Sprintf("rst-%d", time.Now().UnixNano())
	u := users[username]
	u.ResetToken = token
	users[username] = u

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
	defer usersMu.Unlock()

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
		http.Error(w, "Invalid or expired token", http.StatusBadRequest)
		return
	}

	u := users[username]
	hashed := hashPassword(req.Password, u.Salt)
	u.Password = hashed
	u.ResetToken = "" // invalidate token
	u.FailedAttempts = 0
	u.LockedUntil = time.Time{}
	users[username] = u

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
		session.Revoked = true
	}
	sessionsMu.Unlock()

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

	// Simple mock verification: any 6-digit matching "123456" succeeds
	if req.Code == "123456" {
		user.MFAEnabled = true
		users[userKey] = user
		usersMu.Unlock()

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
	}
	usersMu.Unlock()

	claims := map[string]interface{}{
		"sub": username,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}
	claimsBytes, _ := json.Marshal(claims)
	token := base64Encode(claimsBytes)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":       "success",
		"username":     username,
		"access_token": token,
	})
}
