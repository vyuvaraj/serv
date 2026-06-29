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
