package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vyuvaraj/ServShared"
	"golang.org/x/crypto/bcrypt"

	"servauth/pkg/kms"
	"servauth/pkg/oauth"
	"servauth/pkg/sessions"
	"servauth/pkg/store"
)

var (
	Users      = make(map[string]store.User) // key: username
	UsersMu    sync.RWMutex
	APIKeys    = make(map[string]*store.APIKey) // key: token/key
	APIKeysMu  sync.RWMutex
	Clients    = map[string]string{
		"console-client-id": "console-secret-key-9876",
	}

	// JWKS Keys
	JWTRSAPrivateKey *rsa.PrivateKey
	JWTRSAPublicKey  *rsa.PublicKey
	JWTKeyID         = "servverse-key-v1"

	JWKKeyPairs   []store.JWKKeyPair
	JWKKeyPairsMu sync.RWMutex

	UserStore store.UserStore
)

// HashPassword hashes password using bcrypt
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// VerifyPassword checks password against bcrypt hash
func VerifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func InitStore() {
	client := ServShared.NewStoreClient()
	UserStore = store.NewServStoreUserStore(client)
	LoadStateFromStore()
}

func LoadStateFromStore() {
	if u, err := UserStore.LoadUsers(); err == nil {
		UsersMu.Lock()
		Users = u
		UsersMu.Unlock()
	}
	if k, err := UserStore.LoadKeys(); err == nil {
		APIKeysMu.Lock()
		APIKeys = k
		APIKeysMu.Unlock()
	}
	if s, err := UserStore.LoadSessions(); err == nil {
		sessions.SessionsMu.Lock()
		sessions.Sessions = s
		sessions.SessionsMu.Unlock()
	}
}

func SaveUsersToStore() {
	if UserStore == nil {
		return
	}
	UsersMu.RLock()
	copied := make(map[string]store.User)
	for k, v := range Users {
		copied[k] = v
	}
	UsersMu.RUnlock()
	_ = UserStore.SaveUsers(copied)
}

func SaveAPIKeysToStore() {
	if UserStore == nil {
		return
	}
	APIKeysMu.RLock()
	copied := make(map[string]*store.APIKey)
	for k, v := range APIKeys {
		copied[k] = v
	}
	APIKeysMu.RUnlock()
	_ = UserStore.SaveKeys(copied)
}

func SaveSessionsToStore() {
	if UserStore == nil {
		return
	}
	sessions.SessionsMu.RLock()
	copied := make(map[string]*store.Session)
	for k, v := range sessions.Sessions {
		copied[k] = v
	}
	sessions.SessionsMu.RUnlock()
	_ = UserStore.SaveSessions(copied)
}

func InitJWKS() {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("failed to generate RSA key pair: %v", err)
	}
	pub := &priv.PublicKey
	kid := fmt.Sprintf("servverse-key-%d", time.Now().UnixNano())

	JWKKeyPairsMu.Lock()
	JWKKeyPairs = append(JWKKeyPairs, store.JWKKeyPair{
		KeyID:      kid,
		PrivateKey: priv,
		PublicKey:  pub,
		CreatedAt:  time.Now(),
	})
	JWTRSAPrivateKey = priv
	JWTRSAPublicKey = pub
	JWTKeyID = kid
	JWKKeyPairsMu.Unlock()
}

func HandleRegister(w http.ResponseWriter, r *http.Request) {
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

	UsersMu.Lock()
	if _, exists := Users[userKey]; exists {
		UsersMu.Unlock()
		http.Error(w, "Username already exists in this tenant", http.StatusConflict)
		return
	}

	saltBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, saltBytes); err != nil {
		UsersMu.Unlock()
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	salt := hex.EncodeToString(saltBytes)

	hashedPassword, err := HashPassword(req.Password)
	if err != nil {
		UsersMu.Unlock()
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

	Users[userKey] = newUser
	UsersMu.Unlock()

	SaveUsersToStore()
	_ = ServShared.EmitAuditEvent("ServAuth", "USER_REGISTER", req.Username, map[string]interface{}{"email": req.Email, "tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newUser)
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
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

	UsersMu.Lock()
	user, exists := Users[userKey]
	if exists && !user.LockedUntil.IsZero() && user.LockedUntil.After(time.Now()) {
		UsersMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"lockout","message":"Account is locked due to multiple failed login attempts."}`))
		return
	}
	UsersMu.Unlock()

	if !exists {
		_ = ServShared.EmitAuditEvent("ServAuth", "LOGIN_FAILED", req.Username, map[string]interface{}{
			"ip":     r.RemoteAddr,
			"reason": "user_not_found",
			"tenant": tenantID,
		})
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	if !VerifyPassword(req.Password, user.Password) {
		// AI.32 track stuffing IP
		ip := r.RemoteAddr
		if idx := strings.Index(ip, ":"); idx > 0 {
			ip = ip[:idx]
		}
		sessions.RecordFailedLogin(ip)

		UsersMu.Lock()
		u := Users[userKey]
		u.FailedAttempts++
		if u.FailedAttempts >= 3 {
			u.LockedUntil = time.Now().Add(5 * time.Minute)
			_ = ServShared.EmitAuditEvent("ServAuth", "ACCOUNT_LOCKED", req.Username, map[string]interface{}{
				"ip":              r.RemoteAddr,
				"failed_attempts": u.FailedAttempts,
				"locked_until":    u.LockedUntil.UTC().Format(time.RFC3339),
				"tenant":          tenantID,
			})
		}
		Users[userKey] = u
		UsersMu.Unlock()
		SaveUsersToStore()
		_ = ServShared.EmitAuditEvent("ServAuth", "LOGIN_FAILED", req.Username, map[string]interface{}{
			"ip":     r.RemoteAddr,
			"reason": "invalid_password",
			"tenant": tenantID,
		})
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	// Reset attempts on success
	UsersMu.Lock()
	u := Users[userKey]
	u.FailedAttempts = 0
	u.LockedUntil = time.Time{}
	Users[userKey] = u
	UsersMu.Unlock()
	SaveUsersToStore()

	// Generate JWT using ServShared Secret or default test key
	secret := os.Getenv("SERV_JWT_SECRET")
	if secret == "" {
		secret = "test-secret-key-12345"
	}

	var token string
	var err error
	if os.Getenv("SERV_JWKS_URL") != "" || os.Getenv("SERV_JWT_SIGNING_METHOD") == "RS256" {
		token, err = ServShared.GenerateUserTokenRS256(JWTRSAPrivateKey, JWTKeyID, user.Username, []string{"user"}, tenantID, 24*time.Hour)
	} else {
		token, err = ServShared.GenerateUserToken(secret, user.Username, []string{"user"}, tenantID, 24*time.Hour)
	}
	if err != nil {
		http.Error(w, "Token generation failed", http.StatusInternalServerError)
		return
	}

	sessions.SessionsMu.Lock()
	sessions.Sessions[token] = &store.Session{
		Token:     token,
		Username:  user.Username,
		IP:        r.RemoteAddr,
		UserAgent: r.UserAgent(),
		CreatedAt: time.Now(),
		Revoked:   false,
	}
	sessions.SessionsMu.Unlock()
	SaveSessionsToStore()
	_ = sessions.EnterpriseRegisterAuthSession(token, user.Username, r.RemoteAddr, r.UserAgent())
	_ = ServShared.EmitAuditEvent("ServAuth", "USER_LOGIN", user.Username, map[string]interface{}{"ip": r.RemoteAddr, "tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(store.LoginResponse{
		Token:    token,
		Username: user.Username,
	})
}

func HandleToken(w http.ResponseWriter, r *http.Request) {
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

	if !oauth.ValidateClient(clientID, clientSecret, Clients) {
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
		tokenObj.Header["kid"] = JWTKeyID
		token, err = tokenObj.SignedString(JWTRSAPrivateKey)
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
	w.Write(fmt.Appendf(nil, `{"access_token":"%s","token_type":"Bearer","expires_in":3600}`, token))
}

func HandleResetRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req store.ResetRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	UsersMu.Lock()
	found := false
	var username string
	for name, user := range Users {
		if user.Email == req.Email {
			found = true
			username = name
			break
		}
	}

	if !found {
		UsersMu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"Reset link sent if email exists"}`))
		return
	}

	token := fmt.Sprintf("rst-%d", time.Now().UnixNano())
	u := Users[username]
	u.ResetToken = token
	Users[username] = u
	UsersMu.Unlock()
	SaveUsersToStore()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "token": token})
}

func HandleResetConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req store.ResetConfirm
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	UsersMu.Lock()
	found := false
	var username string
	for name, user := range Users {
		if user.ResetToken != "" && user.ResetToken == req.Token {
			found = true
			username = name
			break
		}
	}

	if !found {
		UsersMu.Unlock()
		http.Error(w, "Invalid or expired token", http.StatusBadRequest)
		return
	}

	u := Users[username]
	hashed, err := HashPassword(req.Password)
	if err != nil {
		UsersMu.Unlock()
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	u.Password = hashed
	u.ResetToken = "" // invalidate token
	u.FailedAttempts = 0
	u.LockedUntil = time.Time{}
	Users[username] = u
	UsersMu.Unlock()
	SaveUsersToStore()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Password updated successfully"}`))
}

func HandleKeys(w http.ResponseWriter, r *http.Request) {
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

	APIKeysMu.Lock()
	APIKeys[hashedKey] = apiKey
	APIKeysMu.Unlock()
	SaveAPIKeysToStore()

	_ = ServShared.EmitAuditEvent("ServAuth", "API_KEY_ISSUE", req.Username, map[string]interface{}{"scopes": req.Scopes})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":      hexKey, // Return plaintext key only once to the registering client
		"username": req.Username,
		"scopes":   req.Scopes,
	})
}

func HandleKeysValidate(w http.ResponseWriter, r *http.Request) {
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

	APIKeysMu.RLock()
	apiKey, exists := APIKeys[hashedKey]
	APIKeysMu.RUnlock()

	if !exists || apiKey.Revoked {
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return
	}

	_ = ServShared.EmitAuditEvent("ServAuth", "API_KEY_VALIDATE", apiKey.Username, map[string]interface{}{"scopes": apiKey.Scopes})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(apiKey)
}

func HandleKeysRevoke(w http.ResponseWriter, r *http.Request) {
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

	APIKeysMu.Lock()
	apiKey, exists := APIKeys[hashedKey]
	if exists {
		apiKey.Revoked = true
	}
	APIKeysMu.Unlock()

	if !exists {
		http.Error(w, "API key not found", http.StatusNotFound)
		return
	}

	SaveAPIKeysToStore()
	_ = ServShared.EmitAuditEvent("ServAuth", "API_KEY_REVOKE", apiKey.Username, map[string]interface{}{"scopes": apiKey.Scopes})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"API key revoked"}`))
}

func HandleSessionsRevoke(w http.ResponseWriter, r *http.Request) {
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

	sessions.SessionsMu.Lock()
	session, exists := sessions.Sessions[req.Token]
	if exists {
		if sessions.IsSessionExpired(session) {
			exists = false
		} else {
			session.Revoked = true
		}
	}
	sessions.SessionsMu.Unlock()
	SaveSessionsToStore()
	_ = sessions.EnterpriseRevokeAuthSession(req.Token)

	if !exists {
		http.Error(w, "store.Session not found", http.StatusNotFound)
		return
	}

	_ = ServShared.EmitAuditEvent("ServAuth", "SESSION_REVOKE", session.Username, map[string]interface{}{"token": req.Token})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"store.Session revoked successfully"}`))
}

func HandleMfaSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if ActiveMfaEngine == nil {
		http.Error(w, "MFA is an Enterprise Edition feature.", http.StatusNotImplemented)
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

	UsersMu.Lock()
	user, exists := Users[userKey]
	if !exists {
		UsersMu.Unlock()
		http.Error(w, "store.User not found", http.StatusNotFound)
		return
	}

	secret, qrCodeURL, err := ActiveMfaEngine.Setup(req.Username)
	if err != nil {
		UsersMu.Unlock()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	user.MFASecret = secret
	Users[userKey] = user
	UsersMu.Unlock()
	SaveUsersToStore()

	_ = ServShared.EmitAuditEvent("ServAuth", "MFA_SETUP", req.Username, map[string]interface{}{"tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"secret":      secret,
		"issuer":      "Servverse",
		"account":     req.Username,
		"qr_mock_url": qrCodeURL,
	})
}

func HandleMfaVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if ActiveMfaEngine == nil {
		http.Error(w, "MFA is an Enterprise Edition feature.", http.StatusNotImplemented)
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

	UsersMu.Lock()
	user, exists := Users[userKey]
	if !exists {
		UsersMu.Unlock()
		http.Error(w, "store.User not found", http.StatusNotFound)
		return
	}

	// Verify the TOTP code via the active engine
	if ActiveMfaEngine.Verify(user.MFASecret, req.Code) {
		user.MFAEnabled = true
		Users[userKey] = user
		UsersMu.Unlock()
		SaveUsersToStore()

		_ = ServShared.EmitAuditEvent("ServAuth", "MFA_VERIFY", req.Username, map[string]interface{}{"tenant": tenantID})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"MFA verified and enabled successfully"}`))
		return
	}

	UsersMu.Unlock()
	http.Error(w, "Invalid verification code", http.StatusUnauthorized)
}

// SocialAuthProvider defines dynamic hooks for third-party OAuth logins.
type SocialAuthProvider interface {
	Redirect(w http.ResponseWriter, r *http.Request, provider string)
	Callback(w http.ResponseWriter, r *http.Request, provider, code string) (string, error)
}

// ActiveSocialProvider is the globally registered social OAuth provider hook.
var ActiveSocialProvider SocialAuthProvider

// MfaEngine defines dynamic hooks for MFA TOTP setup and verification.
type MfaEngine interface {
	Setup(username string) (secret string, qrCodeURL string, err error)
	Verify(secret string, code string) bool
}

// ActiveMfaEngine is the globally registered MFA engine hook.
var ActiveMfaEngine MfaEngine


func HandleSocialLogin(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider != "google" && provider != "github" {
		http.Error(w, "Unsupported provider", http.StatusBadRequest)
		return
	}

	if ActiveSocialProvider == nil {
		http.Error(w, "Social authentication is an Enterprise Edition feature.", http.StatusNotImplemented)
		return
	}

	ActiveSocialProvider.Redirect(w, r, provider)
}

func HandleSocialCallback(w http.ResponseWriter, r *http.Request) {
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

	if ActiveSocialProvider == nil {
		http.Error(w, "Social authentication is an Enterprise Edition feature.", http.StatusNotImplemented)
		return
	}

	username, err := ActiveSocialProvider.Callback(w, r, req.Provider, req.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	userKey := username

	UsersMu.Lock()
	_, exists := Users[userKey]
	if !exists {
		Users[userKey] = store.User{
			Username: username,
			Salt:     "salt-social",
			Password: "hash-social",
		}
		UsersMu.Unlock()
		SaveUsersToStore()
	} else {
		UsersMu.Unlock()
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
	if os.Getenv("SERV_JWKS_URL") != "" || os.Getenv("SERV_JWT_SIGNING_METHOD") == "RS256" {
		token, err = ServShared.GenerateUserTokenRS256(JWTRSAPrivateKey, JWTKeyID, username, []string{"user"}, tenantID, 24*time.Hour)
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

func HandleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	UsersMu.RLock()
	var list []map[string]interface{}
	for _, u := range Users {
		if u.TenantID == tenantID {
			list = append(list, map[string]interface{}{
				"username":    u.Username,
				"email":       u.Email,
				"tenant_id":   u.TenantID,
				"mfa_enabled": u.MFAEnabled,
			})
		}
	}
	UsersMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(list)
}

func HandleUsersRoles(w http.ResponseWriter, r *http.Request) {
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

	UsersMu.Lock()
	user, exists := Users[userKey]
	UsersMu.Unlock()

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

func HandleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	list := sessions.GetActiveSessions()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(list)
}

func HandleSecretsEncrypt(w http.ResponseWriter, r *http.Request) {
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

	ciphertext, err := kms.EncryptAES(req.Plaintext)
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

func HandleSecretsDecrypt(w http.ResponseWriter, r *http.Request) {
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

	plaintext, err := kms.DecryptAES(req.Ciphertext)
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

func HandleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	JWKKeyPairsMu.RLock()
	defer JWKKeyPairsMu.RUnlock()

	var keys []map[string]interface{}
	for _, pair := range JWKKeyPairs {
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

func HandleRotateJWKS(w http.ResponseWriter, r *http.Request) {
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

	JWKKeyPairsMu.Lock()
	JWKKeyPairs = append(JWKKeyPairs, store.JWKKeyPair{
		KeyID:      kid,
		PrivateKey: priv,
		PublicKey:  pub,
		CreatedAt:  time.Now(),
	})
	JWTRSAPrivateKey = priv
	JWTRSAPublicKey = pub
	JWTKeyID = kid
	JWKKeyPairsMu.Unlock()

	_ = ServShared.EmitAuditEvent("ServAuth", "JWKS_ROTATE", "system", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(fmt.Appendf(nil, `{"status":"success","rotated_to":"%s"}`, kid))
}

func HandleAdaptiveRiskScore(w http.ResponseWriter, r *http.Request) {
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
	_, _ = w.Write(fmt.Appendf(nil, `{"risk_score":%.2f,"require_mfa":%t}`, risk, requireMFA))
}

func HandleCredentialStuffing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	stuffingIPs := sessions.GetStuffingIPs()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stuffing_detected": len(stuffingIPs) > 0,
		"flagged_ips":        stuffingIPs,
	})
}
