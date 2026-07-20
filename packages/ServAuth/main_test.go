package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServShared"
	"github.com/vyuvaraj/serv/packages/ServShared/pkg/middleware"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/handlers"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/kms"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/mfa"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/sessions"
	"github.com/vyuvaraj/serv/packages/ServAuth/pkg/store"
)

func generateTOTP(secret string) string {
	currentTime := time.Now().Unix()
	step := int64(30)
	counter := currentTime / step
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(counter))

	key := []byte(secret)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	hs := mac.Sum(nil)

	offset := hs[len(hs)-1] & 0x0f
	binCode := int(hs[offset]&0x7f)<<24 |
		int(hs[offset+1]&0xff)<<16 |
		int(hs[offset+2]&0xff)<<8 |
		int(hs[offset+3]&0xff)

	otp := binCode % 1000000
	return fmt.Sprintf("%06d", otp)
}

func setupTest() {
	handlers.UsersMu.Lock()
	handlers.Users = make(map[string]store.User)
	handlers.UsersMu.Unlock()

	handlers.APIKeysMu.Lock()
	handlers.APIKeys = make(map[string]*store.APIKey)
	handlers.APIKeysMu.Unlock()

	sessions.SessionsMu.Lock()
	sessions.Sessions = make(map[string]*store.Session)
	sessions.SessionsMu.Unlock()
}

func TestServAuthWorkflow(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handlers.HandleRegister)
	mux.HandleFunc("/api/auth/login", handlers.HandleLogin)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register store.User
	registerPayload := store.RegisterRequest{
		Username: "testuser",
		Email:    "test@example.com",
		Password: "mysecurepassword",
	}
	body, _ := json.Marshal(registerPayload)
	resp, err := http.Post(testServer.URL+"/api/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post register request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected StatusCreated, got %d", resp.StatusCode)
	}

	var user store.User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		t.Fatalf("failed to decode registered user response: %v", err)
	}

	if user.Username != "testuser" || user.Email != "test@example.com" {
		t.Errorf("expected username and email to match register payload, got %+v", user)
	}

	// 2. Login store.User
	loginPayload := store.LoginRequest{
		Username: "testuser",
		Password: "mysecurepassword",
	}
	loginBody, _ := json.Marshal(loginPayload)
	loginResp, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("failed to post login request: %v", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected StatusOK on login, got %d", loginResp.StatusCode)
	}

	var loginResponse store.LoginResponse
	if err := json.NewDecoder(loginResp.Body).Decode(&loginResponse); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	if loginResponse.Username != "testuser" || loginResponse.Token == "" {
		t.Errorf("expected valid login response with token, got %+v", loginResponse)
	}
}

func TestServAuthOAuthToken(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", handlers.HandleToken)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Post client credentials to oauth/token
	payload := map[string]string{
		"client_id":     "console-client-id",
		"client_secret": "console-secret-key-9876",
		"grant_type":    "client_credentials",
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(testServer.URL+"/oauth/token", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post token request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected StatusOK, got %d", resp.StatusCode)
	}

	var res struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res.AccessToken == "" || res.TokenType != "Bearer" {
		t.Errorf("expected access token and Bearer type, got %+v", res)
	}
}

func TestServAuthSecurityLockoutAndReset(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handlers.HandleRegister)
	mux.HandleFunc("/api/auth/login", handlers.HandleLogin)
	mux.HandleFunc("/api/auth/reset-password/request", handlers.HandleResetRequest)
	mux.HandleFunc("/api/auth/reset-password/confirm", handlers.HandleResetConfirm)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register a user
	regPayload := store.RegisterRequest{
		Username: "lockuser",
		Email:    "lock@example.com",
		Password: "correctpassword",
	}
	body, _ := json.Marshal(regPayload)
	resp, _ := http.Post(testServer.URL+"/api/auth/register", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// 2. Perform 3 failed logins to trigger lockout
	loginPayload := store.LoginRequest{
		Username: "lockuser",
		Password: "wrongpassword",
	}
	loginBody, _ := json.Marshal(loginPayload)

	for i := 0; i < 3; i++ {
		loginResp, _ := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
		loginResp.Body.Close()
	}

	// 4th login attempt (even with CORRECT password) should fail with StatusForbidden (lockout)
	successPayload := store.LoginRequest{
		Username: "lockuser",
		Password: "correctpassword",
	}
	successBody, _ := json.Marshal(successPayload)
	lockResp, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(successBody))
	if err != nil {
		t.Fatalf("failed login: %v", err)
	}
	defer lockResp.Body.Close()

	if lockResp.StatusCode != http.StatusForbidden {
		t.Errorf("expected StatusForbidden on locked account, got %d", lockResp.StatusCode)
	}

	// 3. Request Password Reset
	resetReq := store.ResetRequest{Email: "lock@example.com"}
	resetReqBody, _ := json.Marshal(resetReq)
	resetResp, err := http.Post(testServer.URL+"/api/auth/reset-password/request", "application/json", bytes.NewReader(resetReqBody))
	if err != nil {
		t.Fatalf("failed reset request: %v", err)
	}
	defer resetResp.Body.Close()

	var resetRes struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}
	json.NewDecoder(resetResp.Body).Decode(&resetRes)

	if resetRes.Token == "" {
		t.Fatalf("expected reset token, got empty")
	}

	// 4. Confirm Password Reset with new password
	confirmReq := store.ResetConfirm{
		Token:    resetRes.Token,
		Password: "newpassword123",
	}
	confirmBody, _ := json.Marshal(confirmReq)
	confirmResp, err := http.Post(testServer.URL+"/api/auth/reset-password/confirm", "application/json", bytes.NewReader(confirmBody))
	if err != nil {
		t.Fatalf("failed reset confirm: %v", err)
	}
	confirmResp.Body.Close()

	if confirmResp.StatusCode != http.StatusOK {
		t.Fatalf("expected StatusOK on reset confirm, got %d", confirmResp.StatusCode)
	}

	// 5. Test login with new password (should be unlocked and succeed!)
	newLoginPayload := store.LoginRequest{
		Username: "lockuser",
		Password: "newpassword123",
	}
	newLoginBody, _ := json.Marshal(newLoginPayload)
	newLoginResp, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(newLoginBody))
	if err != nil {
		t.Fatalf("failed login: %v", err)
	}
	defer newLoginResp.Body.Close()

	if newLoginResp.StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK on login after reset, got %d", newLoginResp.StatusCode)
	}
}

func TestServAuthKeysAndSessions(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handlers.HandleRegister)
	mux.HandleFunc("/api/auth/login", handlers.HandleLogin)
	mux.HandleFunc("/api/auth/keys", handlers.HandleKeys)
	mux.HandleFunc("/api/auth/keys/validate", handlers.HandleKeysValidate)
	mux.HandleFunc("/api/auth/sessions/revoke", handlers.HandleSessionsRevoke)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Generate API Key
	payloadKey := map[string]interface{}{
		"username": "service-account-alice",
		"scopes":   []string{"read:metrics", "write:deployments"},
	}
	bodyKey, _ := json.Marshal(payloadKey)
	respKey, err := http.Post(testServer.URL+"/api/auth/keys", "application/json", bytes.NewReader(bodyKey))
	if err != nil {
		t.Fatalf("failed API key generation: %v", err)
	}
	defer respKey.Body.Close()

	if respKey.StatusCode != http.StatusCreated {
		t.Fatalf("expected StatusCreated, got %d", respKey.StatusCode)
	}

	var keyRes struct {
		Key      string   `json:"key"`
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
	}
	json.NewDecoder(respKey.Body).Decode(&keyRes)

	if keyRes.Key == "" || keyRes.Username != "service-account-alice" {
		t.Fatalf("invalid generated key: %+v", keyRes)
	}

	// 2. Validate API Key
	payloadVal := map[string]string{"key": keyRes.Key}
	bodyVal, _ := json.Marshal(payloadVal)
	respVal, err := http.Post(testServer.URL+"/api/auth/keys/validate", "application/json", bytes.NewReader(bodyVal))
	if err != nil {
		t.Fatalf("failed API key validation request: %v", err)
	}
	defer respVal.Body.Close()

	if respVal.StatusCode != http.StatusOK {
		t.Fatalf("expected key validation StatusOK, got %d", respVal.StatusCode)
	}

	var valRes store.APIKey
	json.NewDecoder(respVal.Body).Decode(&valRes)
	if valRes.Username != "service-account-alice" || valRes.Scopes[0] != "read:metrics" {
		t.Errorf("unexpected scopes validation: %+v", valRes)
	}

	// 3. Register user and login to create a store.Session
	regPayload := store.RegisterRequest{
		Username: "sessionuser",
		Email:    "session@example.com",
		Password: "password123",
	}
	regBody, _ := json.Marshal(regPayload)
	regResp, err := http.Post(testServer.URL+"/api/auth/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("failed register request: %v", err)
	}
	regResp.Body.Close()

	loginPayload := store.LoginRequest{
		Username: "sessionuser",
		Password: "password123",
	}
	loginBody, _ := json.Marshal(loginPayload)
	loginResp, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("failed login request: %v", err)
	}
	var loginRes store.LoginResponse
	json.NewDecoder(loginResp.Body).Decode(&loginRes)
	loginResp.Body.Close()

	if loginRes.Token == "" {
		t.Fatalf("expected login token for session tracking")
	}

	// 4. Revoke store.Session
	revPayload := map[string]string{"token": loginRes.Token}
	revBody, _ := json.Marshal(revPayload)
	revResp, err := http.Post(testServer.URL+"/api/auth/sessions/revoke", "application/json", bytes.NewReader(revBody))
	if err != nil {
		t.Fatalf("failed session revocation request: %v", err)
	}
	defer revResp.Body.Close()

	if revResp.StatusCode != http.StatusOK {
		t.Errorf("expected session revocation StatusOK, got %d", revResp.StatusCode)
	}
}

type mockMfaEngine struct{}

func (m *mockMfaEngine) Setup(username string) (string, string, error) {
	mockSecret := "secret-totp-key-for-" + username
	qrCodeURL := "https://api.qrserver.com/v1/create-qr-code/?data=otpauth://totp/Servverse:" + username
	return mockSecret, qrCodeURL, nil
}

func (m *mockMfaEngine) Verify(secret string, code string) bool {
	return mfa.VerifyTOTP(secret, code)
}

func TestServAuthTenancyAndMfa(t *testing.T) {
	setupTest()
	handlers.ActiveMfaEngine = &mockMfaEngine{}
	defer func() { handlers.ActiveMfaEngine = nil }()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handlers.HandleRegister)
	mux.HandleFunc("/api/auth/login", handlers.HandleLogin)
	mux.HandleFunc("/api/auth/mfa/setup", handlers.HandleMfaSetup)
	mux.HandleFunc("/api/auth/mfa/verify", handlers.HandleMfaVerify)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register same username under two different tenants -> both should succeed!
	regPayload := store.RegisterRequest{
		Username: "multitenant-bob",
		Email:    "bob@example.com",
		Password: "password123",
	}
	body, _ := json.Marshal(regPayload)

	req1, _ := http.NewRequest(http.MethodPost, testServer.URL+"/api/auth/register", bytes.NewReader(body))
	req1.Header.Set("X-Tenant-ID", "tenant-alpha")
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil || resp1.StatusCode != http.StatusCreated {
		t.Fatalf("failed to register Bob in tenant-alpha: %v", err)
	}
	resp1.Body.Close()

	req2, _ := http.NewRequest(http.MethodPost, testServer.URL+"/api/auth/register", bytes.NewReader(body))
	req2.Header.Set("X-Tenant-ID", "tenant-beta")
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil || resp2.StatusCode != http.StatusCreated {
		t.Fatalf("failed to register Bob in tenant-beta: %v", err)
	}
	resp2.Body.Close()

	// 2. Setup MFA
	mfaReqBody, _ := json.Marshal(map[string]string{"username": "multitenant-bob"})
	reqMfa, _ := http.NewRequest(http.MethodPost, testServer.URL+"/api/auth/mfa/setup", bytes.NewReader(mfaReqBody))
	reqMfa.Header.Set("X-Tenant-ID", "tenant-alpha")
	reqMfa.Header.Set("Content-Type", "application/json")
	respMfa, err := http.DefaultClient.Do(reqMfa)
	if err != nil || respMfa.StatusCode != http.StatusOK {
		t.Fatalf("failed to setup MFA: %v", err)
	}
	var setupRes struct{ Secret string }
	json.NewDecoder(respMfa.Body).Decode(&setupRes)
	respMfa.Body.Close()

	if setupRes.Secret == "" {
		t.Fatalf("expected valid MFA setup secret")
	}

	// 3. Verify MFA
	correctCode := generateTOTP(setupRes.Secret)
	verifyReqBody, _ := json.Marshal(map[string]string{
		"username": "multitenant-bob",
		"code":     correctCode,
	})
	reqVerify, _ := http.NewRequest(http.MethodPost, testServer.URL+"/api/auth/mfa/verify", bytes.NewReader(verifyReqBody))
	reqVerify.Header.Set("X-Tenant-ID", "tenant-alpha")
	reqVerify.Header.Set("Content-Type", "application/json")
	respVerify, err := http.DefaultClient.Do(reqVerify)
	if err != nil || respVerify.StatusCode != http.StatusOK {
		t.Fatalf("failed to verify MFA: %v", err)
	}
	respVerify.Body.Close()
}

type mockSocialProvider struct{}

func (m *mockSocialProvider) Redirect(w http.ResponseWriter, r *http.Request, provider string) {
	redirectURL := fmt.Sprintf("https://auth.provider.com/%s/authorize?client_id=mock-client&redirect_uri=mock-redirect&response_type=code", provider)
	_ = ServShared.EmitAuditEvent("github.com/vyuvaraj/serv/packages/ServAuth", "SOCIAL_LOGIN_REDIRECT", "guest", map[string]interface{}{"provider": provider})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":       "redirect_simulated",
		"redirect_url": redirectURL,
	})
}

func (m *mockSocialProvider) Callback(w http.ResponseWriter, r *http.Request, provider, code string) (string, error) {
	return fmt.Sprintf("social-%s-%s", provider, code[:4]), nil
}

func TestServAuthSocialLogin(t *testing.T) {
	setupTest()
	handlers.ActiveSocialProvider = &mockSocialProvider{}
	defer func() { handlers.ActiveSocialProvider = nil }()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/social/login", handlers.HandleSocialLogin)
	mux.HandleFunc("/api/auth/social/callback", handlers.HandleSocialCallback)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Trigger social login simulation (GET)
	resp, err := http.Get(testServer.URL + "/api/auth/social/login?provider=google")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("social login init failed: %v", err)
	}
	var loginRes map[string]string
	json.NewDecoder(resp.Body).Decode(&loginRes)
	resp.Body.Close()

	if loginRes["status"] != "redirect_simulated" || !strings.Contains(loginRes["redirect_url"], "google") {
		t.Errorf("unexpected social login response: %+v", loginRes)
	}

	// 2. Complete social login callback exchange (POST)
	callbackPayload := map[string]string{
		"provider": "google",
		"code":     "mock-auth-code-12345",
	}
	body, _ := json.Marshal(callbackPayload)
	cbResp, err := http.Post(testServer.URL+"/api/auth/social/callback", "application/json", bytes.NewReader(body))
	if err != nil || cbResp.StatusCode != http.StatusOK {
		t.Fatalf("social callback failed: %v", err)
	}
	var cbRes map[string]string
	json.NewDecoder(cbResp.Body).Decode(&cbRes)
	cbResp.Body.Close()

	if cbRes["status"] != "success" || cbRes["username"] != "social-google-mock" || cbRes["access_token"] == "" {
		t.Errorf("unexpected callback response: %+v", cbRes)
	}
}

func TestServAuthUserMgmtAndSecrets(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handlers.HandleRegister)
	mux.HandleFunc("/api/auth/login", handlers.HandleLogin)
	mux.HandleFunc("/api/auth/users", handlers.HandleUsers)
	mux.HandleFunc("/api/auth/users/roles", handlers.HandleUsersRoles)
	mux.HandleFunc("/api/auth/sessions", handlers.HandleSessions)
	mux.HandleFunc("/api/auth/secrets/encrypt", handlers.HandleSecretsEncrypt)
	mux.HandleFunc("/api/auth/secrets/decrypt", handlers.HandleSecretsDecrypt)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register and login
	regPayload := store.RegisterRequest{
		Username: "mgmt-user",
		Email:    "mgmt@example.com",
		Password: "password123",
	}
	regBody, _ := json.Marshal(regPayload)
	regResp, err := http.Post(testServer.URL+"/api/auth/register", "application/json", bytes.NewReader(regBody))
	if err != nil || regResp.StatusCode != http.StatusCreated {
		t.Fatalf("register failed: %v", err)
	}
	regResp.Body.Close()

	loginBody, _ := json.Marshal(store.LoginRequest{Username: "mgmt-user", Password: "password123"})
	loginResp, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil || loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %v", err)
	}
	loginResp.Body.Close()

	// 2. Query users list -> should contain our new user
	usersResp, err := http.Get(testServer.URL + "/api/auth/users")
	if err != nil || usersResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to query users: %v", err)
	}
	var usersList []map[string]interface{}
	json.NewDecoder(usersResp.Body).Decode(&usersList)
	usersResp.Body.Close()

	found := false
	for _, u := range usersList {
		if u["username"] == "mgmt-user" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find mgmt-user in users list")
	}

	// 3. Assign role/scopes
	rolePayload := map[string]interface{}{
		"username": "mgmt-user",
		"scopes":   []string{"admin", "read:all"},
	}
	roleBody, _ := json.Marshal(rolePayload)
	roleResp, err := http.Post(testServer.URL+"/api/auth/users/roles", "application/json", bytes.NewReader(roleBody))
	if err != nil || roleResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to update user roles: %v", err)
	}
	roleResp.Body.Close()

	// 4. Query active sessions -> should contain a session
	sessionsResp, err := http.Get(testServer.URL + "/api/auth/sessions")
	if err != nil || sessionsResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to query sessions: %v", err)
	}
	var sessionsList []store.Session
	json.NewDecoder(sessionsResp.Body).Decode(&sessionsList)
	sessionsResp.Body.Close()

	if len(sessionsList) == 0 {
		t.Errorf("expected at least one active session")
	}

	// 5. Test secrets encrypt/decrypt
	encPayload := map[string]string{"plaintext": "super-secret-credentials"}
	encBody, _ := json.Marshal(encPayload)
	encResp, err := http.Post(testServer.URL+"/api/auth/secrets/encrypt", "application/json", bytes.NewReader(encBody))
	if err != nil || encResp.StatusCode != http.StatusOK {
		t.Fatalf("encrypt failed: %v", err)
	}
	var encRes struct {
		Ciphertext       string `json:"ciphertext"`
		EncryptedDataKey string `json:"encrypted_data_key"`
	}
	json.NewDecoder(encResp.Body).Decode(&encRes)
	encResp.Body.Close()

	if encRes.Ciphertext == "" || encRes.EncryptedDataKey == "" {
		t.Errorf("invalid encryption response: %+v", encRes)
	}

	decPayload := map[string]string{
		"ciphertext":         encRes.Ciphertext,
		"encrypted_data_key": encRes.EncryptedDataKey,
	}
	decBody, _ := json.Marshal(decPayload)
	decResp, err := http.Post(testServer.URL+"/api/auth/secrets/decrypt", "application/json", bytes.NewReader(decBody))
	if err != nil || decResp.StatusCode != http.StatusOK {
		t.Fatalf("decrypt failed: %v", err)
	}
	var decRes struct {
		Plaintext string `json:"plaintext"`
	}
	json.NewDecoder(decResp.Body).Decode(&decRes)
	decResp.Body.Close()

	if decRes.Plaintext != "super-secret-credentials" {
		t.Errorf("expected decrypted plaintext 'super-secret-credentials', got %q", decRes.Plaintext)
	}
}

func TestServAuthSecurityFeatures(t *testing.T) {
	setupTest()
	// 1. Test Bcrypt hashing
	hash, err := handlers.HashPassword("mySecretPassword")
	if err != nil {
		t.Fatalf("bcrypt hashing failed: %v", err)
	}
	if !handlers.VerifyPassword("mySecretPassword", hash) {
		t.Errorf("bcrypt verification failed for correct password")
	}
	if handlers.VerifyPassword("wrongPassword", hash) {
		t.Errorf("bcrypt verification succeeded for incorrect password")
	}

	// 2. Test AES-GCM encryption/decryption
	originalText := "sensitive-information"
	ciphertext, err := kms.EncryptAES(originalText)
	if err != nil {
		t.Fatalf("AES-GCM encryption failed: %v", err)
	}
	decryptedText, err := kms.DecryptAES(ciphertext)
	if err != nil {
		t.Fatalf("AES-GCM decryption failed: %v", err)
	}
	if decryptedText != originalText {
		t.Errorf("expected decrypted text %q, got %q", originalText, decryptedText)
	}

	// 3. Test TOTP verification
	mfaSecret := "secret-totp-key-for-test-user"
	code := generateTOTP(mfaSecret)
	if !mfa.VerifyTOTP(mfaSecret, code) {
		t.Errorf("TOTP verification failed for correct current code")
	}
	if mfa.VerifyTOTP(mfaSecret, "000000") {
		t.Errorf("TOTP verification succeeded for invalid code")
	}

	// 4. Test store.Session Expiry helper
	freshSession := &store.Session{CreatedAt: time.Now()}
	expiredSession := &store.Session{CreatedAt: time.Now().Add(-25 * time.Hour)}
	if sessions.IsSessionExpired(freshSession) {
		t.Errorf("fresh session should not be expired")
	}
	if !sessions.IsSessionExpired(expiredSession) {
		t.Errorf("session older than 24 hours should be expired")
	}
}

func TestTableDrivenKeyValidation(t *testing.T) {
	setupTest()

	// Pre-populate some keys
	handlers.APIKeysMu.Lock()
	testKeyBytes := sha256.Sum256([]byte("valid-key-id"))
	testKeyHex := hex.EncodeToString(testKeyBytes[:])
	handlers.APIKeys[testKeyHex] = &store.APIKey{
		Key:       testKeyHex,
		Username:  "user-a",
		CreatedAt: time.Now(),
	}
	handlers.APIKeysMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/keys/validate", handlers.HandleKeysValidate)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	tests := []struct {
		name       string
		key        string
		wantStatus int
	}{
		{
			name:       "Valid Key",
			key:        "valid-key-id",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Non-existent Key",
			key:        "missing-key-id",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqPayload := struct {
				Key string `json:"key"`
			}{
				Key: tt.key,
			}
			body, _ := json.Marshal(reqPayload)
			resp, err := http.Post(testServer.URL+"/api/auth/keys/validate", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestAPIKeyRevocation(t *testing.T) {
	setupTest()

	// 1. Register API key
	reqPayload := `{"username":"alice","scopes":["read"]}`
	req := httptest.NewRequest("POST", "/api/auth/keys", strings.NewReader(reqPayload))
	w := httptest.NewRecorder()
	handlers.HandleKeys(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}

	var registerRes struct {
		Key string `json:"key"`
	}
	json.NewDecoder(w.Body).Decode(&registerRes)

	// 2. Validate it works
	valPayload := fmt.Sprintf(`{"key":%q}`, registerRes.Key)
	reqVal1 := httptest.NewRequest("POST", "/api/auth/keys/validate", strings.NewReader(valPayload))
	wVal1 := httptest.NewRecorder()
	handlers.HandleKeysValidate(wVal1, reqVal1)
	if wVal1.Code != http.StatusOK {
		t.Errorf("expected 200 on initial validation, got %d", wVal1.Code)
	}

	// 3. Revoke the API key
	revPayload := fmt.Sprintf(`{"key":%q}`, registerRes.Key)
	reqRev := httptest.NewRequest("POST", "/api/auth/keys/revoke", strings.NewReader(revPayload))
	wRev := httptest.NewRecorder()
	handlers.HandleKeysRevoke(wRev, reqRev)
	if wRev.Code != http.StatusOK {
		t.Fatalf("expected 200 on revoke, got %d", wRev.Code)
	}

	// 4. Validate again (should be 401 Unauthorized now!)
	reqVal2 := httptest.NewRequest("POST", "/api/auth/keys/validate", strings.NewReader(valPayload))
	wVal2 := httptest.NewRecorder()
	handlers.HandleKeysValidate(wVal2, reqVal2)
	if wVal2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized after key revocation, got %d", wVal2.Code)
	}
}

func TestTraceparentPropagation(t *testing.T) {
	setupTest()

	// Initialize tracing
	ServShared.InitTrace("servauth-test")

	// Set up the trace middleware wrapped handler
	handler := ServShared.TraceMiddleware("github.com/vyuvaraj/serv/packages/ServAuth", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Inside the handler, traceparent header must be set
		tp := r.Header.Get("traceparent")
		if !strings.Contains(tp, "4fa3b1234567890abcdef1234567890a") {
			t.Errorf("Expected traceparent to contain trace ID 4fa3b1234567890abcdef1234567890a, got %q", tp)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/auth/config", nil)
	req.Header.Set("traceparent", "00-4fa3b1234567890abcdef1234567890a-1122334455667788-01")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	// Verify the response header propagates it too
	respTp := w.Header().Get("traceparent")
	if !strings.Contains(respTp, "4fa3b1234567890abcdef1234567890a") {
		t.Errorf("Expected response traceparent to contain trace ID, got %q", respTp)
	}
}

func BenchmarkAPIKeyHashing(b *testing.B) {
	key := "test-api-key-12345-sec"
	b.ResetTimer()
	for b.Loop() {
		h := sha256.Sum256([]byte(key))
		_ = h
	}
}

func BenchmarkAPIKeyValidation(b *testing.B) {
	handlers.APIKeysMu.Lock()
	testHash := fmt.Sprintf("%x", sha256.Sum256([]byte("valid-key-id")))
	handlers.APIKeys[testHash] = &store.APIKey{
		Key:       "valid-key-id",
		Username:  "test-user",
		CreatedAt: time.Now(),
	}
	handlers.APIKeysMu.Unlock()

	b.ResetTimer()
	for b.Loop() {
		key := "valid-key-id"
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
		handlers.APIKeysMu.RLock()
		_, _ = handlers.APIKeys[hash]
		handlers.APIKeysMu.RUnlock()
	}
}

type mockStuffingDetector struct{}

func (m *mockStuffingDetector) Detect() (bool, []string) {
	return true, []string{"192.168.1.100"}
}

func TestPluggableStuffingDetector(t *testing.T) {
	// 1. Without hook registered: returns 501
	req1 := httptest.NewRequest("GET", "/api/auth/stuffing", nil)
	rr1 := httptest.NewRecorder()
	handlers.HandleCredentialStuffing(rr1, req1)

	if rr1.Code != http.StatusNotImplemented {
		t.Errorf("expected 501 Not Implemented, got %d", rr1.Code)
	}

	// 2. With hook registered: returns 200 with mock results
	mock := &mockStuffingDetector{}
	handlers.ActiveStuffingDetector = mock
	defer func() { handlers.ActiveStuffingDetector = nil }()

	req2 := httptest.NewRequest("GET", "/api/auth/stuffing", nil)
	rr2 := httptest.NewRecorder()
	handlers.HandleCredentialStuffing(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rr2.Code)
	}

	var resp struct {
		StuffingDetected bool     `json:"stuffing_detected"`
		FlaggedIPs       []string `json:"flagged_ips"`
	}
	json.NewDecoder(rr2.Body).Decode(&resp)

	if !resp.StuffingDetected || len(resp.FlaggedIPs) != 1 || resp.FlaggedIPs[0] != "192.168.1.100" {
		t.Errorf("unexpected stuffing response: %+v", resp)
	}
}

func TestTimingAttackResistance(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handlers.HandleRegister)
	mux.HandleFunc("/api/auth/login", handlers.HandleLogin)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register an existing user
	regPayload := store.RegisterRequest{
		Username: "timinguser",
		Email:    "timing@example.com",
		Password: "correctpassword",
	}
	body, _ := json.Marshal(regPayload)
	resp, _ := http.Post(testServer.URL+"/api/auth/register", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// 2. Measure login time for EXISTING user with WRONG password
	loginPayloadExistent := store.LoginRequest{
		Username: "timinguser",
		Password: "wrongpassword",
	}
	bodyExistent, _ := json.Marshal(loginPayloadExistent)

	startExistent := time.Now()
	respExistent, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(bodyExistent))
	if err != nil {
		t.Fatalf("failed existent login request: %v", err)
	}
	respExistent.Body.Close()
	durExistent := time.Since(startExistent)

	// 3. Measure login time for NON-EXISTENT user
	loginPayloadNonExistent := store.LoginRequest{
		Username: "nobody-exists-with-this-name",
		Password: "wrongpassword",
	}
	bodyNonExistent, _ := json.Marshal(loginPayloadNonExistent)

	startNonExistent := time.Now()
	respNonExistent, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(bodyNonExistent))
	if err != nil {
		t.Fatalf("failed non-existent login request: %v", err)
	}
	respNonExistent.Body.Close()
	durNonExistent := time.Since(startNonExistent)

	t.Logf("Existent user (wrong pass) dur: %v", durExistent)
	t.Logf("Non-existent user dur: %v", durNonExistent)

	// Difference should be very small (e.g. less than 20ms slack on modern CPUs)
	diff := durExistent - durNonExistent
	if diff < 0 {
		diff = -diff
	}

	if diff > 30*time.Millisecond {
		t.Errorf("potential timing attack: difference between existent and non-existent user login is too large (%v)", diff)
	}
}

func TestTokenRefreshRaceCondition(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", handlers.HandleToken)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Get initial access and refresh token
	resp, err := http.PostForm(testServer.URL+"/oauth/token", url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"console-client-id"},
		"client_secret": {"console-secret-key-9876"},
	})
	if err != nil {
		t.Fatalf("failed client credentials token request: %v", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	t.Logf("Response status: %d, body: %s", resp.StatusCode, buf.String())

	var tokenRes struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	json.Unmarshal(buf.Bytes(), &tokenRes)

	if tokenRes.RefreshToken == "" {
		t.Fatalf("expected refresh token, got empty")
	}

	// 2. Perform concurrent refresh requests with same token
	var wg sync.WaitGroup
	var mu sync.Mutex
	statusCodes := []int{}

	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			r, err := http.PostForm(testServer.URL+"/oauth/token", url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {tokenRes.RefreshToken},
			})
			if err == nil {
				mu.Lock()
				statusCodes = append(statusCodes, r.StatusCode)
				mu.Unlock()
				r.Body.Close()
			}
		}()
	}

	wg.Wait()

	// One should succeed (200) and the other should be rejected (401 / 400)
	successCount := 0
	failCount := 0
	for _, status := range statusCodes {
		if status == http.StatusOK {
			successCount++
		} else {
			failCount++
		}
	}

	t.Logf("Concurrent refresh results: success=%d, failures=%d", successCount, failCount)
	if successCount != 1 || failCount != 1 {
		t.Errorf("expected exactly 1 success and 1 failure, got %d successes and %d failures", successCount, failCount)
	}
}

func TestSessionRevocationPropagation(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/users", handlers.HandleUsers)
	mux.HandleFunc("/api/auth/sessions/revoke", handlers.HandleSessionsRevoke)

	// Wrap in middleware chain
	serverHandler := handlers.RevocationMiddleware(mux)

	testServer := httptest.NewServer(serverHandler)
	defer testServer.Close()

	// 1. Setup session directly
	token := "revokable-token-xyz"
	sessions.SessionsMu.Lock()
	sessions.Sessions[token] = &store.Session{
		Token:     token,
		Username:  "sessionuser",
		CreatedAt: time.Now(),
		Revoked:   false,
	}
	sessions.SessionsMu.Unlock()

	// 2. Access protected endpoint -> should not be revoked
	req1, _ := http.NewRequest("GET", testServer.URL+"/api/auth/users", nil)
	req1.Header.Set("Authorization", "Bearer "+token)
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode == http.StatusUnauthorized {
		var errResp map[string]string
		json.NewDecoder(resp1.Body).Decode(&errResp)
		if errResp["code"] == "ERR_SESSION_REVOKED" {
			t.Errorf("expected session not to be revoked yet")
		}
	}

	// 3. Revoke session
	revokeBody := fmt.Sprintf(`{"token":"%s"}`, token)
	respRev, err := http.Post(testServer.URL+"/api/auth/sessions/revoke", "application/json", bytes.NewReader([]byte(revokeBody)))
	if err != nil {
		t.Fatalf("revocation call failed: %v", err)
	}
	respRev.Body.Close()

	// 4. Access protected endpoint again -> must fail immediately with StatusUnauthorized
	req2, _ := http.NewRequest("GET", testServer.URL+"/api/auth/users", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized, got %d", resp2.StatusCode)
	}

	var errResp map[string]string
	json.NewDecoder(resp2.Body).Decode(&errResp)
	if errResp["code"] != "ERR_SESSION_REVOKED" {
		t.Errorf("expected ERR_SESSION_REVOKED error code, got %s", errResp["code"])
	}
}

// TestTOTPTimeDrift (D.41) verifies that TOTP codes are accepted within +-1 step (30s) drift,
// but strictly rejected at +-2 steps drift.
func TestTOTPTimeDrift(t *testing.T) {
	secret := "secretkey12345"

	// Helper to generate TOTP code at a specific time offset
	gen := func(timeOffset int64) string {
		currentTime := time.Now().Unix() + timeOffset
		step := int64(30)
		counter := currentTime / step
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(counter))

		mac := hmac.New(sha1.New, []byte(secret))
		mac.Write(buf)
		hs := mac.Sum(nil)

		offset := hs[len(hs)-1] & 0x0f
		binCode := int(hs[offset]&0x7f)<<24 |
			int(hs[offset+1]&0xff)<<16 |
			int(hs[offset+2]&0xff)<<8 |
			int(hs[offset+3]&0xff)

		otp := binCode % 1000000
		return fmt.Sprintf("%06d", otp)
	}

	// 1. t (0 offset) -> must be valid
	codeT := gen(0)
	if !mfa.VerifyTOTP(secret, codeT) {
		t.Errorf("expected TOTP code at t to be valid")
	}

	// 2. t-1 (-30s offset) -> must be valid
	codeTMinus1 := gen(-30)
	if !mfa.VerifyTOTP(secret, codeTMinus1) {
		t.Errorf("expected TOTP code at t-1 (drift -30s) to be valid")
	}

	// 3. t+1 (+30s offset) -> must be valid
	codeTPlus1 := gen(30)
	if !mfa.VerifyTOTP(secret, codeTPlus1) {
		t.Errorf("expected TOTP code at t+1 (drift +30s) to be valid")
	}

	// 4. t-2 (-60s offset) -> must be REJECTED
	codeTMinus2 := gen(-60)
	if mfa.VerifyTOTP(secret, codeTMinus2) {
		t.Errorf("expected TOTP code at t-2 (drift -60s) to be rejected")
	}

	// 5. t+2 (+60s offset) -> must be REJECTED
	codeTPlus2 := gen(60)
	if mfa.VerifyTOTP(secret, codeTPlus2) {
		t.Errorf("expected TOTP code at t+2 (drift +60s) to be rejected")
	}
}

func TestMagicLinkFlow(t *testing.T) {
	setupTest()

	regPayload := `{"username":"magic_user","email":"magic@example.com","password":"Password123!"}`
	reqReg := httptest.NewRequest("POST", "/api/auth/register", strings.NewReader(regPayload))
	reqReg.Header.Set("Content-Type", "application/json")
	wReg := httptest.NewRecorder()
	handlers.HandleRegister(wReg, reqReg)
	if wReg.Code != http.StatusOK && wReg.Code != http.StatusCreated {
		t.Fatalf("failed to register user: %d", wReg.Code)
	}

	// 1. Request magic link
	reqLink := httptest.NewRequest("POST", "/api/auth/magic-link/request", strings.NewReader(`{"email":"magic@example.com"}`))
	reqLink.Header.Set("Content-Type", "application/json")
	wLink := httptest.NewRecorder()
	handlers.HandleMagicLinkRequest(wLink, reqLink)
	if wLink.Code != http.StatusOK {
		t.Fatalf("magic link request failed: %d", wLink.Code)
	}

	var linkResp struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}
	json.NewDecoder(wLink.Body).Decode(&linkResp)

	if linkResp.Status != "success" || linkResp.Token == "" {
		t.Fatalf("invalid magic link response: %+v", linkResp)
	}

	// 2. Verify magic link token
	reqVerify := httptest.NewRequest("GET", "/api/auth/magic-link/verify?token="+linkResp.Token, nil)
	wVerify := httptest.NewRecorder()
	handlers.HandleMagicLinkVerify(wVerify, reqVerify)
	if wVerify.Code != http.StatusOK {
		t.Fatalf("magic link verification failed: %d", wVerify.Code)
	}

	var loginResp struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	json.NewDecoder(wVerify.Body).Decode(&loginResp)

	if loginResp.Username != "magic_user" || loginResp.Token == "" {
		t.Errorf("invalid login token response: %+v", loginResp)
	}
}

func TestPasskeysFlow(t *testing.T) {
	setupTest()

	regPayload := `{"username":"passkey_user","email":"pk@example.com","password":"Password123!"}`
	reqReg := httptest.NewRequest("POST", "/api/auth/register", strings.NewReader(regPayload))
	reqReg.Header.Set("Content-Type", "application/json")
	wReg := httptest.NewRecorder()
	handlers.HandleRegister(wReg, reqReg)
	if wReg.Code != http.StatusOK && wReg.Code != http.StatusCreated {
		t.Fatalf("failed to register user: %d", wReg.Code)
	}

	// 1. Request registration challenge
	reqChal := httptest.NewRequest("POST", "/api/auth/passkey/register/challenge", nil)
	ctx := context.WithValue(reqChal.Context(), middleware.ClaimsContextKey, &middleware.Claims{Username: "passkey_user"})
	reqChal = reqChal.WithContext(ctx)
	wChal := httptest.NewRecorder()
	handlers.HandlePasskeyRegisterChallenge(wChal, reqChal)
	if wChal.Code != http.StatusOK {
		t.Fatalf("register challenge failed: %d", wChal.Code)
	}

	var chalResp struct {
		Challenge string `json:"challenge"`
	}
	json.NewDecoder(wChal.Body).Decode(&chalResp)

	// 2. Verify registration assertion
	verifyPayload := fmt.Sprintf(`{"challenge":%q,"credential_id":"cred-123","public_key":"pubkey-xyz"}`, chalResp.Challenge)
	reqVer := httptest.NewRequest("POST", "/api/auth/passkey/register/verify", strings.NewReader(verifyPayload))
	reqVer.Header.Set("Content-Type", "application/json")
	reqVer = reqVer.WithContext(ctx)
	wVer := httptest.NewRecorder()
	handlers.HandlePasskeyRegisterVerify(wVer, reqVer)
	if wVer.Code != http.StatusOK {
		t.Fatalf("register verify failed: %d", wVer.Code)
	}

	// 3. Request login challenge
	reqLoginChal := httptest.NewRequest("POST", "/api/auth/passkey/login/challenge", strings.NewReader(`{"username":"passkey_user"}`))
	reqLoginChal.Header.Set("Content-Type", "application/json")
	wLoginChal := httptest.NewRecorder()
	handlers.HandlePasskeyLoginChallenge(wLoginChal, reqLoginChal)
	if wLoginChal.Code != http.StatusOK {
		t.Fatalf("login challenge failed: %d", wLoginChal.Code)
	}

	var loginChalResp struct {
		Challenge string `json:"challenge"`
	}
	json.NewDecoder(wLoginChal.Body).Decode(&loginChalResp)

	// 4. Verify login verification (logs in!)
	loginVerifyPayload := fmt.Sprintf(`{"username":"passkey_user","challenge":%q,"signature":"sig-abc"}`, loginChalResp.Challenge)
	reqLoginVer := httptest.NewRequest("POST", "/api/auth/passkey/login/verify", strings.NewReader(loginVerifyPayload))
	reqLoginVer.Header.Set("Content-Type", "application/json")
	wLoginVer := httptest.NewRecorder()
	handlers.HandlePasskeyLoginVerify(wLoginVer, reqLoginVer)
	if wLoginVer.Code != http.StatusOK {
		t.Fatalf("login verify failed: %d", wLoginVer.Code)
	}

	var loginResp struct {
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	json.NewDecoder(wLoginVer.Body).Decode(&loginResp)
	if loginResp.Username != "passkey_user" || loginResp.Token == "" {
		t.Errorf("expected successful login with passkey, got %+v", loginResp)
	}
}

func TestSCIM20Provisioning(t *testing.T) {
	setupTest()

	// 1. Create a User via SCIM POST
	scimPost := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"scim_user","emails":[{"value":"scim@example.com","primary":true}]}`
	reqPost := httptest.NewRequest("POST", "/scim/v2/Users", strings.NewReader(scimPost))
	reqPost.Header.Set("Content-Type", "application/json")
	wPost := httptest.NewRecorder()
	handlers.HandleSCIMUsers(wPost, reqPost)
	if wPost.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from SCIM POST, got %d", wPost.Code)
	}

	// 2. Get User via SCIM GET
	reqGet := httptest.NewRequest("GET", "/scim/v2/Users/scim_user", nil)
	wGet := httptest.NewRecorder()
	handlers.HandleSCIMUsers(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from SCIM GET, got %d", wGet.Code)
	}
	var retrievedUser handlers.SCIMUser
	json.NewDecoder(wGet.Body).Decode(&retrievedUser)
	if retrievedUser.UserName != "scim_user" || len(retrievedUser.Emails) == 0 || retrievedUser.Emails[0].Value != "scim@example.com" {
		t.Errorf("retrieved user mismatch: %+v", retrievedUser)
	}

	// 3. Update User via SCIM PUT
	scimPut := `{"emails":[{"value":"updated_scim@example.com","primary":true}]}`
	reqPut := httptest.NewRequest("PUT", "/scim/v2/Users/scim_user", strings.NewReader(scimPut))
	reqPut.Header.Set("Content-Type", "application/json")
	wPut := httptest.NewRecorder()
	handlers.HandleSCIMUsers(wPut, reqPut)
	if wPut.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from SCIM PUT, got %d", wPut.Code)
	}

	// 4. List Users via SCIM GET (without ID suffix)
	reqList := httptest.NewRequest("GET", "/scim/v2/Users", nil)
	wList := httptest.NewRecorder()
	handlers.HandleSCIMUsers(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from SCIM List, got %d", wList.Code)
	}
	var listResp handlers.SCIMListResponse
	json.NewDecoder(wList.Body).Decode(&listResp)
	if listResp.TotalResults < 1 {
		t.Errorf("expected list to return users, got totalResults %d", listResp.TotalResults)
	}

	// 5. Delete User via SCIM DELETE
	reqDelete := httptest.NewRequest("DELETE", "/scim/v2/Users/scim_user", nil)
	wDelete := httptest.NewRecorder()
	handlers.HandleSCIMUsers(wDelete, reqDelete)
	if wDelete.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content from SCIM DELETE, got %d", wDelete.Code)
	}
}

func TestAdaptiveRiskScoring(t *testing.T) {
	setupTest()

	regPayload := `{"username":"risk_user","email":"risk@example.com","password":"Password123!"}`
	reqReg := httptest.NewRequest("POST", "/api/auth/register", strings.NewReader(regPayload))
	reqReg.Header.Set("Content-Type", "application/json")
	wReg := httptest.NewRecorder()
	handlers.HandleRegister(wReg, reqReg)
	if wReg.Code != http.StatusOK && wReg.Code != http.StatusCreated {
		t.Fatalf("failed to register user: %d", wReg.Code)
	}

	// 1. Initial login to set historical context (Device=MacBook, Country=US)
	payload1 := `{"username":"risk_user","device":"MacBook","country":"US","hour":12}`
	req1 := httptest.NewRequest("POST", "/api/auth/risk", strings.NewReader(payload1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	handlers.HandleAdaptiveRiskScore(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from risk handler, got %d", w1.Code)
	}

	// 2. Safe login: identical device/country, normal hour (12) -> should have risk score 0
	payload2 := `{"username":"risk_user","device":"MacBook","country":"US","hour":12}`
	req2 := httptest.NewRequest("POST", "/api/auth/risk", strings.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handlers.HandleAdaptiveRiskScore(w2, req2)
	var resp2 struct {
		RiskScore  int  `json:"risk_score"`
		RequireMFA bool `json:"require_mfa"`
	}
	json.NewDecoder(w2.Body).Decode(&resp2)
	if resp2.RiskScore != 0 || resp2.RequireMFA {
		t.Errorf("expected 0 risk score for safe login, got score %d, require_mfa %t", resp2.RiskScore, resp2.RequireMFA)
	}

	// 3. Risky login: new device (+3), different country (+5), unusual hour (+2) -> total 10, requires MFA
	payload3 := `{"username":"risk_user","device":"iPhone","country":"France","hour":2}`
	req3 := httptest.NewRequest("POST", "/api/auth/risk", strings.NewReader(payload3))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	handlers.HandleAdaptiveRiskScore(w3, req3)
	var resp3 struct {
		RiskScore  int  `json:"risk_score"`
		RequireMFA bool `json:"require_mfa"`
	}
	json.NewDecoder(w3.Body).Decode(&resp3)
	if resp3.RiskScore != 10 || !resp3.RequireMFA {
		t.Errorf("expected 10 risk score and require_mfa=true for risky login, got score %d, require_mfa %t", resp3.RiskScore, resp3.RequireMFA)
	}
}





