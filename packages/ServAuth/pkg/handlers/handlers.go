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
	"github.com/vyuvaraj/serv/packages/ServShared"
	"golang.org/x/crypto/bcrypt"

	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/kms"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/oauth"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/sessions"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/store"
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
	InitEnterprise()
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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
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
		httpError(w, r, "Username already exists in this tenant", http.StatusConflict)
		return
	}

	saltBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, saltBytes); err != nil {
		UsersMu.Unlock()
		httpError(w, r, "Internal server error", http.StatusInternalServerError)
		return
	}
	salt := hex.EncodeToString(saltBytes)

	hashedPassword, err := HashPassword(req.Password)
	if err != nil {
		UsersMu.Unlock()
		httpError(w, r, "Internal server error", http.StatusInternalServerError)
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
	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "USER_REGISTER", req.Username, map[string]interface{}{"email": req.Email, "tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newUser)
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
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

	// Timing attack prevention: always perform bcrypt check to ensure uniform response timing
	var hashToCheck string
	if exists {
		hashToCheck = user.Password
	} else {
		hashToCheck = "$2a$10$S3XqY4QJ.y3R1/c/83X/ueQ56xQn4zO3x.Bv6u/56xQn4zO3x.Bv6"
	}
	passwordMatches := VerifyPassword(req.Password, hashToCheck)

	if !exists {
		_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "LOGIN_FAILED", req.Username, map[string]interface{}{
			"ip":     r.RemoteAddr,
			"reason": "user_not_found",
			"tenant": tenantID,
		})
		httpError(w, r, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	if !passwordMatches {
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
			_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "ACCOUNT_LOCKED", req.Username, map[string]interface{}{
				"ip":              r.RemoteAddr,
				"failed_attempts": u.FailedAttempts,
				"locked_until":    u.LockedUntil.UTC().Format(time.RFC3339),
				"tenant":          tenantID,
			})
		}
		Users[userKey] = u
		UsersMu.Unlock()
		SaveUsersToStore()
		_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "LOGIN_FAILED", req.Username, map[string]interface{}{
			"ip":     r.RemoteAddr,
			"reason": "invalid_password",
			"tenant": tenantID,
		})
		httpError(w, r, "Invalid username or password", http.StatusUnauthorized)
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
		httpError(w, r, "Token generation failed", http.StatusInternalServerError)
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
	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "USER_LOGIN", user.Username, map[string]interface{}{"ip": r.RemoteAddr, "tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(store.LoginResponse{
		Token:    token,
		Username: user.Username,
	})
}

func HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
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

	if grantType == "refresh_token" {
		// 1. Get refresh token
		refreshToken := r.FormValue("refresh_token")
		if refreshToken == "" && r.Header.Get("Content-Type") == "application/json" {
			var req struct {
				RefreshToken string `json:"refresh_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			refreshToken = req.RefreshToken
		}

		if refreshToken == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid_request","message":"refresh_token is required"}`))
			return
		}

		// 2. Lock to prevent race condition
		sessions.SessionsMu.Lock()
		session, exists := sessions.Sessions[refreshToken]
		if !exists || session.Revoked || sessions.IsSessionExpired(session) {
			sessions.SessionsMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid_grant","message":"refresh token is invalid, expired, or already used"}`))
			return
		}

		// Revoke the old refresh token immediately (Token Rotation) to prevent reuse/race condition
		session.Revoked = true
		sessions.SessionsMu.Unlock()

		SaveSessionsToStore()

		// 3. Generate new tokens
		secret := os.Getenv("SERV_JWT_SECRET")
		if secret == "" {
			secret = "test-secret-key-12345"
		}
		tenantID := r.Header.Get("X-Tenant-ID")

		newAccessToken, _ := ServShared.GenerateUserToken(secret, session.Username, []string{"user"}, tenantID, 1*time.Hour)
		newRefreshToken, _ := ServShared.GenerateUserToken(secret, session.Username, []string{"user"}, tenantID, 24*time.Hour)

		// 4. Save new session/refresh token
		sessions.SessionsMu.Lock()
		sessions.Sessions[newRefreshToken] = &store.Session{
			Token:     newRefreshToken,
			Username:  session.Username,
			IP:        r.RemoteAddr,
			UserAgent: r.UserAgent(),
			CreatedAt: time.Now(),
			Revoked:   false,
		}
		sessions.SessionsMu.Unlock()
		SaveSessionsToStore()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(fmt.Appendf(nil, `{"access_token":"%s","refresh_token":"%s","token_type":"Bearer","expires_in":3600}`, newAccessToken, newRefreshToken))
		return
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
			Issuer:    "github.com/vyuvaraj/serv/packages/ServAuth",
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
		httpError(w, r, "Token generation failed", http.StatusInternalServerError)
		return
	}

	sessions.SessionsMu.Lock()
	sessions.Sessions[token] = &store.Session{
		Token:     token,
		Username:  clientID,
		IP:        r.RemoteAddr,
		UserAgent: r.UserAgent(),
		CreatedAt: time.Now(),
		Revoked:   false,
	}
	sessions.SessionsMu.Unlock()
	SaveSessionsToStore()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(fmt.Appendf(nil, `{"access_token":"%s","refresh_token":"%s","token_type":"Bearer","expires_in":3600}`, token, token))
}

func HandleResetRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
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
		httpError(w, r, "Invalid or expired token", http.StatusBadRequest)
		return
	}

	u := Users[username]
	hashed, err := HashPassword(req.Password)
	if err != nil {
		UsersMu.Unlock()
		httpError(w, r, "Internal server error", http.StatusInternalServerError)
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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
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

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "API_KEY_ISSUE", req.Username, map[string]interface{}{"scopes": req.Scopes})

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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
		return
	}

	// SEC.8: Hash the incoming validate request key before looking it up
	hashedBytes := sha256.Sum256([]byte(req.Key))
	hashedKey := hex.EncodeToString(hashedBytes[:])

	APIKeysMu.RLock()
	apiKey, exists := APIKeys[hashedKey]
	APIKeysMu.RUnlock()

	if !exists || apiKey.Revoked {
		httpError(w, r, "Invalid API key", http.StatusUnauthorized)
		return
	}

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "API_KEY_VALIDATE", apiKey.Username, map[string]interface{}{"scopes": apiKey.Scopes})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(apiKey)
}

func HandleKeysRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
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
		httpError(w, r, "API key not found", http.StatusNotFound)
		return
	}

	SaveAPIKeysToStore()
	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "API_KEY_REVOKE", apiKey.Username, map[string]interface{}{"scopes": apiKey.Scopes})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"API key revoked"}`))
}

func HandleSessionsRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
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
		httpError(w, r, "store.Session not found", http.StatusNotFound)
		return
	}

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "SESSION_REVOKE", session.Username, map[string]interface{}{"token": req.Token})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"store.Session revoked successfully"}`))
}

func HandleMfaSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if ActiveMfaEngine == nil {
		httpError(w, r, "MFA is an Enterprise Edition feature.", http.StatusNotImplemented)
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
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
		httpError(w, r, "store.User not found", http.StatusNotFound)
		return
	}

	secret, qrCodeURL, err := ActiveMfaEngine.Setup(req.Username)
	if err != nil {
		UsersMu.Unlock()
		httpError(w, r, err.Error(), http.StatusInternalServerError)
		return
	}

	user.MFASecret = secret
	Users[userKey] = user
	UsersMu.Unlock()
	SaveUsersToStore()

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "MFA_SETUP", req.Username, map[string]interface{}{"tenant": tenantID})

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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if ActiveMfaEngine == nil {
		httpError(w, r, "MFA is an Enterprise Edition feature.", http.StatusNotImplemented)
		return
	}

	var req struct {
		Username string `json:"username"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
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
		httpError(w, r, "store.User not found", http.StatusNotFound)
		return
	}

	// Verify the TOTP code via the active engine
	if ActiveMfaEngine.Verify(user.MFASecret, req.Code) {
		user.MFAEnabled = true
		Users[userKey] = user
		UsersMu.Unlock()
		SaveUsersToStore()

		_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "MFA_VERIFY", req.Username, map[string]interface{}{"tenant": tenantID})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"MFA verified and enabled successfully"}`))
		return
	}

	UsersMu.Unlock()
	httpError(w, r, "Invalid verification code", http.StatusUnauthorized)
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
		httpError(w, r, "Unsupported provider", http.StatusBadRequest)
		return
	}

	if ActiveSocialProvider == nil {
		httpError(w, r, "Social authentication is an Enterprise Edition feature.", http.StatusNotImplemented)
		return
	}

	ActiveSocialProvider.Redirect(w, r, provider)
}

func HandleSocialCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Code == "" || len(req.Code) < 4 {
		httpError(w, r, "Invalid auth code", http.StatusBadRequest)
		return
	}

	if ActiveSocialProvider == nil {
		httpError(w, r, "Social authentication is an Enterprise Edition feature.", http.StatusNotImplemented)
		return
	}

	username, err := ActiveSocialProvider.Callback(w, r, req.Provider, req.Code)
	if err != nil {
		httpError(w, r, err.Error(), http.StatusUnauthorized)
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
		httpError(w, r, "Token generation failed", http.StatusInternalServerError)
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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
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
		httpError(w, r, "store.User not found", http.StatusNotFound)
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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	list := sessions.GetActiveSessions()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(list)
}

func HandleSecretsEncrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
		return
	}

	ciphertext, err := kms.EncryptAES(req.Plaintext)
	if err != nil {
		httpError(w, r, "Encryption failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "SECRET_ENCRYPT", "system", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"ciphertext":         ciphertext,
		"encrypted_data_key": "mock-datakey-9876",
	})
}

func HandleSecretsDecrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Ciphertext       string `json:"ciphertext"`
		EncryptedDataKey string `json:"encrypted_data_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, r, "Invalid payload", http.StatusBadRequest)
		return
	}

	plaintext, err := kms.DecryptAES(req.Ciphertext)
	if err != nil {
		httpError(w, r, "Decryption failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "SECRET_DECRYPT", "system", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"plaintext": plaintext,
	})
}

func HandleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
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
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		httpError(w, r, "Key generation failed", http.StatusInternalServerError)
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

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "JWKS_ROTATE", "system", nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(fmt.Appendf(nil, `{"status":"success","rotated_to":"%s"}`, kid))
}

type RiskScoreRequest struct {
	Username string `json:"username"`
	Device   string `json:"device"`
	Country  string `json:"country"`
	Hour     *int   `json:"hour,omitempty"`
}

func (r *RiskScoreRequest) Validate() error {
	return nil
}

func HandleAdaptiveRiskScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req RiskScoreRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}
	if req.Username == "" {
		httpError(w, r, "Username is required", http.StatusBadRequest)
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

	score := 0
	if exists {
		if user.LastDevice != "" && req.Device != "" && user.LastDevice != req.Device {
			score += 3
		}
		if user.LastCountry != "" && req.Country != "" && user.LastCountry != req.Country {
			score += 5
		}
		hour := time.Now().Hour()
		if req.Hour != nil {
			hour = *req.Hour
		}
		if hour >= 0 && hour <= 5 {
			score += 2
		}

		if req.Device != "" || req.Country != "" {
			UsersMu.Lock()
			u := Users[userKey]
			if req.Device != "" {
				u.LastDevice = req.Device
			}
			if req.Country != "" {
				u.LastCountry = req.Country
			}
			Users[userKey] = u
			UsersMu.Unlock()
			SaveUsersToStore()
		}
	} else {
		score = 2
	}

	requireMFA := score >= 5

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"risk_score":%d,"require_mfa":%t}`, score, requireMFA)))
}

// StuffingDetector defines pluggable coordinator hooks for analyzing credential stuffing attempts.
type StuffingDetector interface {
	Detect() (stuffingDetected bool, flaggedIPs []string)
}

// ActiveStuffingDetector is the globally registered credential stuffing detection hook.
var ActiveStuffingDetector StuffingDetector

func HandleCredentialStuffing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if ActiveStuffingDetector == nil {
		httpError(w, r, "Credential stuffing detection is an Enterprise Edition feature.", http.StatusNotImplemented)
		return
	}

	stuffingDetected, flaggedIPs := ActiveStuffingDetector.Detect()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stuffing_detected": stuffingDetected,
		"flagged_ips":        flaggedIPs,
	})
}

func RevocationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			token, err := ServShared.ExtractTokenFromHeader(authHeader)
			if err == nil {
				sessions.SessionsMu.RLock()
				session, exists := sessions.Sessions[token]
				if exists && session.Revoked {
					sessions.SessionsMu.RUnlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte(`{"error":"unauthorized","message":"Session has been revoked.","code":"ERR_SESSION_REVOKED"}`))
					return
				}
				sessions.SessionsMu.RUnlock()
			}
		}
		next.ServeHTTP(w, r)
	})
}

func httpError(w http.ResponseWriter, r *http.Request, error string, code int) {
	var errorCode string
	switch code {
	case http.StatusMethodNotAllowed:
		errorCode = "ERR_METHOD_NOT_ALLOWED"
	case http.StatusBadRequest:
		errorCode = "ERR_BAD_REQUEST"
	case http.StatusUnauthorized:
		errorCode = "ERR_UNAUTHORIZED"
	case http.StatusForbidden:
		errorCode = "ERR_FORBIDDEN"
	case http.StatusNotFound:
		errorCode = "ERR_NOT_FOUND"
	case http.StatusConflict:
		errorCode = "ERR_CONFLICT"
	case http.StatusNotImplemented:
		errorCode = "ERR_NOT_IMPLEMENTED"
	default:
		errorCode = "ERR_INTERNAL_SERVER_ERROR"
	}
	ServShared.WriteJSONError(w, r, error, errorCode, code)
}

// Helper to issue JWT for a user
func issueJWTForUser(w http.ResponseWriter, r *http.Request, username string) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	secret := os.Getenv("SERV_JWT_SECRET")
	if secret == "" {
		secret = "test-secret-key-12345"
	}

	var token string
	var err error
	if os.Getenv("SERV_JWKS_URL") != "" || os.Getenv("SERV_JWT_SIGNING_METHOD") == "RS256" {
		token, err = ServShared.GenerateUserTokenRS256(JWTRSAPrivateKey, JWTKeyID, username, []string{"user"}, tenantID, 24*time.Hour)
	} else {
		token, err = ServShared.GenerateUserToken(secret, username, []string{"user"}, tenantID, 24*time.Hour)
	}
	if err != nil {
		httpError(w, r, "Token generation failed", http.StatusInternalServerError)
		return
	}

	// Create session
	sessions.SessionsMu.Lock()
	sessions.Sessions[token] = &store.Session{
		Token:     token,
		Username:  username,
		IP:        r.RemoteAddr,
		UserAgent: r.UserAgent(),
		CreatedAt: time.Now(),
		Revoked:   false,
	}
	sessions.SessionsMu.Unlock()
	SaveSessionsToStore()

	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "LOGIN_SUCCESS", username, map[string]interface{}{"ip": r.RemoteAddr, "tenant": tenantID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"token":"%s","username":"%s"}`, token, username)))
}

// Magic Link State & Handlers
var (
	magicTokens   = make(map[string]string) // token -> username
	magicTokensMu sync.Mutex
)

func getUserByEmail(email string) (string, bool) {
	UsersMu.RLock()
	defer UsersMu.RUnlock()
	for _, u := range Users {
		if u.Email == email {
			return u.Username, true
		}
	}
	return "", false
}

type MagicLinkRequest struct {
	Email string `json:"email"`
}

func (r *MagicLinkRequest) Validate() error {
	return nil
}

func HandleMagicLinkRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req MagicLinkRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}
	if req.Email == "" {
		httpError(w, r, "Email is required", http.StatusBadRequest)
		return
	}

	username, found := getUserByEmail(req.Email)
	if !found {
		httpError(w, r, "User not found", http.StatusNotFound)
		return
	}

	// Generate a secure token
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		httpError(w, r, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(tokenBytes)

	magicTokensMu.Lock()
	magicTokens[token] = username
	magicTokensMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"status":"success","token":"%s"}`, token)))
}

func HandleMagicLinkVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		var req struct {
			Token string `json:"token"`
		}
		if json.NewDecoder(r.Body).Decode(&req) == nil {
			token = req.Token
		}
	}
	if token == "" {
		httpError(w, r, "Token is required", http.StatusBadRequest)
		return
	}

	magicTokensMu.Lock()
	username, ok := magicTokens[token]
	if ok {
		delete(magicTokens, token)
	}
	magicTokensMu.Unlock()

	if !ok {
		httpError(w, r, "Invalid or expired token", http.StatusUnauthorized)
		return
	}

	issueJWTForUser(w, r, username)
}

// Passkeys (Simulated WebAuthn) State & Handlers
var (
	passkeyChallenges   = make(map[string]string) // username -> challenge
	passkeyChallengesMu sync.Mutex
)

func HandlePasskeyRegisterChallenge(w http.ResponseWriter, r *http.Request) {
	claims := ServShared.GetClaims(r)
	if claims == nil {
		httpError(w, r, "Unauthorized", http.StatusUnauthorized)
		return
	}

	challengeBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, challengeBytes); err != nil {
		httpError(w, r, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes)

	passkeyChallengesMu.Lock()
	passkeyChallenges[claims.Username] = challenge
	passkeyChallengesMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"challenge":"%s"}`, challenge)))
}

type PasskeyRegisterVerifyRequest struct {
	Challenge    string `json:"challenge"`
	CredentialID string `json:"credential_id"`
	PublicKey    string `json:"public_key"`
}

func (r *PasskeyRegisterVerifyRequest) Validate() error {
	return nil
}

func HandlePasskeyRegisterVerify(w http.ResponseWriter, r *http.Request) {
	claims := ServShared.GetClaims(r)
	if claims == nil {
		httpError(w, r, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req PasskeyRegisterVerifyRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	passkeyChallengesMu.Lock()
	savedChallenge, found := passkeyChallenges[claims.Username]
	if found {
		delete(passkeyChallenges, claims.Username)
	}
	passkeyChallengesMu.Unlock()

	if !found || savedChallenge != req.Challenge {
		httpError(w, r, "Invalid challenge", http.StatusBadRequest)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	userKey := tenantID + ":" + claims.Username

	UsersMu.Lock()
	user, exists := Users[userKey]
	if !exists {
		UsersMu.Unlock()
		httpError(w, r, "User not found", http.StatusNotFound)
		return
	}
	user.PasskeyID = req.CredentialID
	user.PasskeyPublicKey = req.PublicKey
	Users[userKey] = user
	UsersMu.Unlock()

	SaveUsersToStore()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

type PasskeyLoginChallengeRequest struct {
	Username string `json:"username"`
}

func (r *PasskeyLoginChallengeRequest) Validate() error {
	return nil
}

func HandlePasskeyLoginChallenge(w http.ResponseWriter, r *http.Request) {
	var req PasskeyLoginChallengeRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}
	if req.Username == "" {
		httpError(w, r, "Username is required", http.StatusBadRequest)
		return
	}

	challengeBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, challengeBytes); err != nil {
		httpError(w, r, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes)

	passkeyChallengesMu.Lock()
	passkeyChallenges[req.Username] = challenge
	passkeyChallengesMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"challenge":"%s"}`, challenge)))
}

type PasskeyLoginVerifyRequest struct {
	Username  string `json:"username"`
	Challenge string `json:"challenge"`
	Signature string `json:"signature"`
}

func (r *PasskeyLoginVerifyRequest) Validate() error {
	return nil
}

func HandlePasskeyLoginVerify(w http.ResponseWriter, r *http.Request) {
	var req PasskeyLoginVerifyRequest
	if !ServShared.DecodeAndValidateJSON(w, r, &req) {
		return
	}

	passkeyChallengesMu.Lock()
	savedChallenge, found := passkeyChallenges[req.Username]
	if found {
		delete(passkeyChallenges, req.Username)
	}
	passkeyChallengesMu.Unlock()

	if !found || savedChallenge != req.Challenge {
		httpError(w, r, "Invalid challenge", http.StatusBadRequest)
		return
	}

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	userKey := tenantID + ":" + req.Username

	UsersMu.RLock()
	user, exists := Users[userKey]
	UsersMu.RUnlock()

	if !exists || user.PasskeyID == "" {
		httpError(w, r, "Passkey not registered for user", http.StatusNotFound)
		return
	}

	if req.Signature == "" {
		httpError(w, r, "Invalid signature", http.StatusUnauthorized)
		return
	}

	issueJWTForUser(w, r, req.Username)
}

// SCIM 2.0 User structures & handlers
type SCIMEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
}

type SCIMUser struct {
	Schemas  []string    `json:"schemas"`
	ID       string      `json:"id"`
	UserName string      `json:"userName"`
	Emails   []SCIMEmail `json:"emails"`
	Active   bool        `json:"active"`
}

type SCIMListResponse struct {
	Schemas      []string   `json:"schemas"`
	TotalResults int        `json:"totalResults"`
	StartIndex   int        `json:"startIndex"`
	ItemsPerPage int        `json:"itemsPerPage"`
	Resources    []SCIMUser `json:"Resources"`
}

func HandleSCIMUsers(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	id := strings.TrimPrefix(path, "/scim/v2/Users")
	id = strings.TrimPrefix(id, "/")

	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	switch r.Method {
	case http.MethodGet:
		if id != "" {
			userKey := tenantID + ":" + id
			UsersMu.RLock()
			user, exists := Users[userKey]
			UsersMu.RUnlock()

			if !exists {
				httpError(w, r, "User not found", http.StatusNotFound)
				return
			}

			scimUser := SCIMUser{
				Schemas:  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
				ID:       user.Username,
				UserName: user.Username,
				Emails: []SCIMEmail{
					{Value: user.Email, Primary: true},
				},
				Active: true,
			}
			w.Header().Set("Content-Type", "application/scim+json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(scimUser)
			return
		}

		UsersMu.RLock()
		var resources []SCIMUser
		for _, user := range Users {
			if user.TenantID == tenantID {
				resources = append(resources, SCIMUser{
					Schemas:  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
					ID:       user.Username,
					UserName: user.Username,
					Emails: []SCIMEmail{
						{Value: user.Email, Primary: true},
					},
					Active: true,
				})
			}
		}
		UsersMu.RUnlock()

		listResp := SCIMListResponse{
			Schemas:      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
			TotalResults: len(resources),
			StartIndex:   1,
			ItemsPerPage: len(resources),
			Resources:    resources,
		}
		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(listResp)

	case http.MethodPost:
		var scimUser SCIMUser
		if err := json.NewDecoder(r.Body).Decode(&scimUser); err != nil {
			httpError(w, r, "Invalid request payload", http.StatusBadRequest)
			return
		}
		if scimUser.UserName == "" {
			httpError(w, r, "userName is required", http.StatusBadRequest)
			return
		}
		email := ""
		if len(scimUser.Emails) > 0 {
			email = scimUser.Emails[0].Value
		}

		userKey := tenantID + ":" + scimUser.UserName

		UsersMu.Lock()
		if _, exists := Users[userKey]; exists {
			UsersMu.Unlock()
			httpError(w, r, "User already exists", http.StatusConflict)
			return
		}

		pwd, _ := HashPassword("SCIMGeneratedPassword123!")

		newUser := store.User{
			Username:  scimUser.UserName,
			Email:     email,
			Password:  pwd,
			CreatedAt: time.Now(),
			TenantID:  tenantID,
		}
		Users[userKey] = newUser
		UsersMu.Unlock()
		SaveUsersToStore()

		scimUser.Schemas = []string{"urn:ietf:params:scim:schemas:core:2.0:User"}
		scimUser.ID = scimUser.UserName
		scimUser.Active = true

		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(scimUser)

	case http.MethodPut:
		if id == "" {
			httpError(w, r, "id is required for update", http.StatusBadRequest)
			return
		}
		var scimUser SCIMUser
		if err := json.NewDecoder(r.Body).Decode(&scimUser); err != nil {
			httpError(w, r, "Invalid request payload", http.StatusBadRequest)
			return
		}

		userKey := tenantID + ":" + id

		UsersMu.Lock()
		user, exists := Users[userKey]
		if !exists {
			UsersMu.Unlock()
			httpError(w, r, "User not found", http.StatusNotFound)
			return
		}

		if len(scimUser.Emails) > 0 {
			user.Email = scimUser.Emails[0].Value
		}
		Users[userKey] = user
		UsersMu.Unlock()
		SaveUsersToStore()

		scimUser.Schemas = []string{"urn:ietf:params:scim:schemas:core:2.0:User"}
		scimUser.ID = id
		scimUser.UserName = id
		scimUser.Active = true

		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(scimUser)

	case http.MethodDelete:
		if id == "" {
			httpError(w, r, "id is required for delete", http.StatusBadRequest)
			return
		}
		userKey := tenantID + ":" + id

		UsersMu.Lock()
		_, exists := Users[userKey]
		if !exists {
			UsersMu.Unlock()
			httpError(w, r, "User not found", http.StatusNotFound)
			return
		}
		delete(Users, userKey)
		UsersMu.Unlock()
		SaveUsersToStore()

		w.WriteHeader(http.StatusNoContent)

	default:
		httpError(w, r, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
