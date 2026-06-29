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
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Password string    `json:"-"`
	Salt     string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
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

var (
	users   = make(map[string]User) // key: username
	usersMu sync.RWMutex
	clients = map[string]string{
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
	mux.HandleFunc("/oauth/token", handleToken)

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

	usersMu.Lock()
	defer usersMu.Unlock()

	if _, exists := users[req.Username]; exists {
		http.Error(w, "Username already exists", http.StatusConflict)
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
	}

	users[req.Username] = newUser

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

	usersMu.RLock()
	user, exists := users[req.Username]
	usersMu.RUnlock()

	if !exists {
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	hashed := hashPassword(req.Password, user.Salt)
	if hashed != user.Password {
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

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
