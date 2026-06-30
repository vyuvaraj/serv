package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	usersMu.Lock()
	users = make(map[string]User)
	usersMu.Unlock()

	apiKeysMu.Lock()
	apiKeys = make(map[string]*APIKey)
	apiKeysMu.Unlock()

	sessionsMu.Lock()
	sessions = make(map[string]*Session)
	sessionsMu.Unlock()
}

func TestServAuthWorkflow(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handleRegister)
	mux.HandleFunc("/api/auth/login", handleLogin)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register User
	registerPayload := RegisterRequest{
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

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		t.Fatalf("failed to decode registered user response: %v", err)
	}

	if user.Username != "testuser" || user.Email != "test@example.com" {
		t.Errorf("expected username and email to match register payload, got %+v", user)
	}

	// 2. Login User
	loginPayload := LoginRequest{
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

	var loginResponse LoginResponse
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
	mux.HandleFunc("/oauth/token", handleToken)

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
	mux.HandleFunc("/api/auth/register", handleRegister)
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/reset-password/request", handleResetRequest)
	mux.HandleFunc("/api/auth/reset-password/confirm", handleResetConfirm)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register a user
	regPayload := RegisterRequest{
		Username: "lockuser",
		Email:    "lock@example.com",
		Password: "correctpassword",
	}
	body, _ := json.Marshal(regPayload)
	resp, _ := http.Post(testServer.URL+"/api/auth/register", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// 2. Perform 3 failed logins to trigger lockout
	loginPayload := LoginRequest{
		Username: "lockuser",
		Password: "wrongpassword",
	}
	loginBody, _ := json.Marshal(loginPayload)

	for i := 0; i < 3; i++ {
		loginResp, _ := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
		loginResp.Body.Close()
	}

	// 4th login attempt (even with CORRECT password) should fail with StatusForbidden (lockout)
	successPayload := LoginRequest{
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
	resetReq := ResetRequest{Email: "lock@example.com"}
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
	confirmReq := ResetConfirm{
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
	newLoginPayload := LoginRequest{
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
	mux.HandleFunc("/api/auth/register", handleRegister)
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/keys", handleKeys)
	mux.HandleFunc("/api/auth/keys/validate", handleKeysValidate)
	mux.HandleFunc("/api/auth/sessions/revoke", handleSessionsRevoke)

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

	var valRes APIKey
	json.NewDecoder(respVal.Body).Decode(&valRes)
	if valRes.Username != "service-account-alice" || valRes.Scopes[0] != "read:metrics" {
		t.Errorf("unexpected scopes validation: %+v", valRes)
	}

	// 3. Register user and login to create a Session
	regPayload := RegisterRequest{
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

	loginPayload := LoginRequest{
		Username: "sessionuser",
		Password: "password123",
	}
	loginBody, _ := json.Marshal(loginPayload)
	loginResp, err := http.Post(testServer.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("failed login request: %v", err)
	}
	var loginRes LoginResponse
	json.NewDecoder(loginResp.Body).Decode(&loginRes)
	loginResp.Body.Close()

	if loginRes.Token == "" {
		t.Fatalf("expected login token for session tracking")
	}

	// 4. Revoke Session
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

func TestServAuthTenancyAndMfa(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/register", handleRegister)
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/mfa/setup", handleMfaSetup)
	mux.HandleFunc("/api/auth/mfa/verify", handleMfaVerify)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register same username under two different tenants -> both should succeed!
	regPayload := RegisterRequest{
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

func TestServAuthSocialLogin(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/social/login", handleSocialLogin)
	mux.HandleFunc("/api/auth/social/callback", handleSocialCallback)

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
	mux.HandleFunc("/api/auth/register", handleRegister)
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/users", handleUsers)
	mux.HandleFunc("/api/auth/users/roles", handleUsersRoles)
	mux.HandleFunc("/api/auth/sessions", handleSessions)
	mux.HandleFunc("/api/auth/secrets/encrypt", handleSecretsEncrypt)
	mux.HandleFunc("/api/auth/secrets/decrypt", handleSecretsDecrypt)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Register and login
	regPayload := RegisterRequest{
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

	loginBody, _ := json.Marshal(LoginRequest{Username: "mgmt-user", Password: "password123"})
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
	var sessionsList []Session
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
	hash, err := hashPassword("mySecretPassword")
	if err != nil {
		t.Fatalf("bcrypt hashing failed: %v", err)
	}
	if !verifyPassword("mySecretPassword", hash) {
		t.Errorf("bcrypt verification failed for correct password")
	}
	if verifyPassword("wrongPassword", hash) {
		t.Errorf("bcrypt verification succeeded for incorrect password")
	}

	// 2. Test AES-GCM encryption/decryption
	originalText := "sensitive-information"
	ciphertext, err := encryptAES(originalText)
	if err != nil {
		t.Fatalf("AES-GCM encryption failed: %v", err)
	}
	decryptedText, err := decryptAES(ciphertext)
	if err != nil {
		t.Fatalf("AES-GCM decryption failed: %v", err)
	}
	if decryptedText != originalText {
		t.Errorf("expected decrypted text %q, got %q", originalText, decryptedText)
	}

	// 3. Test TOTP verification
	mfaSecret := "secret-totp-key-for-test-user"
	code := generateTOTP(mfaSecret)
	if !verifyTOTP(mfaSecret, code) {
		t.Errorf("TOTP verification failed for correct current code")
	}
	if verifyTOTP(mfaSecret, "000000") {
		t.Errorf("TOTP verification succeeded for invalid code")
	}

	// 4. Test Session Expiry helper
	freshSession := &Session{CreatedAt: time.Now()}
	expiredSession := &Session{CreatedAt: time.Now().Add(-25 * time.Hour)}
	if isSessionExpired(freshSession) {
		t.Errorf("fresh session should not be expired")
	}
	if !isSessionExpired(expiredSession) {
		t.Errorf("session older than 24 hours should be expired")
	}
}

func TestTableDrivenKeyValidation(t *testing.T) {
	setupTest()

	// Pre-populate some keys
	apiKeysMu.Lock()
	testKeyBytes := sha256.Sum256([]byte("valid-key-id"))
	testKeyHex := hex.EncodeToString(testKeyBytes[:])
	apiKeys[testKeyHex] = &APIKey{
		Key:       testKeyHex,
		Username:  "user-a",
		CreatedAt: time.Now(),
	}
	apiKeysMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/keys/validate", handleKeysValidate)
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

