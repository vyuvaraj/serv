package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServAuthWorkflow(t *testing.T) {
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
