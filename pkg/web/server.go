package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

//go:embed assets/*
var assetsFS embed.FS

type WebConsole struct {
	gateway    http.Handler
	fileServer http.Handler
	auth       *auth.AuthProvider
	store      storage.StorageEngine
}

func NewWebConsole(gateway http.Handler, authProvider *auth.AuthProvider, store storage.StorageEngine) *WebConsole {
	// Strip assets prefix
	subFS, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}

	return &WebConsole{
		gateway:    gateway,
		fileServer: http.FileServer(http.FS(subFS)),
		auth:       authProvider,
		store:      store,
	}
}

func (wc *WebConsole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	accept := r.Header.Get("Accept")

	// 1. Handle auth endpoints
	if path == "/console/auth-config" {
		w.Header().Set("Content-Type", "application/json")
		hasLDAP := false
		hasOIDC := false
		if wc.auth.IsEnabled() {
			hasLDAP = wc.auth.HasLDAP()
			hasOIDC = wc.auth.HasOIDC()
		}

		configMap := map[string]bool{
			"oidcEnabled": hasOIDC,
			"ldapEnabled": wc.auth.IsEnabled() || hasLDAP,
		}
		json.NewEncoder(w).Encode(configMap)
		return
	}

	if path == "/console/login" && r.Method == http.MethodPost {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		token, err := wc.auth.AuthenticateConsole(req.Username, req.Password)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Set cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "token",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400, // 24 hours
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": token})
		return
	}

	if path == "/console/oauth/login" {
		if oidc := wc.auth.OIDCClient(); oidc != nil && oidc.IsEnabled() {
			// Generate state
			stateBytes := make([]byte, 16)
			rand.Read(stateBytes)
			state := hex.EncodeToString(stateBytes)

			// Store state in a cookie for validation
			http.SetCookie(w, &http.Cookie{
				Name:     "oidc_state",
				Value:    state,
				Path:     "/",
				HttpOnly: true,
				Secure:   r.TLS != nil,
				MaxAge:   300, // 5 minutes
			})

			authURL, err := oidc.GetAuthURL(state)
			if err != nil {
				http.Error(w, "OIDC auth URL generation failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, authURL, http.StatusFound)
			return
		}
		http.Error(w, "OIDC not configured", http.StatusBadRequest)
		return
	}

	if path == "/console/oauth/callback" {
		if oidc := wc.auth.OIDCClient(); oidc != nil && oidc.IsEnabled() {
			// Verify state
			stateCookie, err := r.Cookie("oidc_state")
			if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
				http.Error(w, "Invalid state parameter", http.StatusBadRequest)
				return
			}

			// Clear state cookie
			http.SetCookie(w, &http.Cookie{
				Name:   "oidc_state",
				Value:  "",
				Path:   "/",
				MaxAge: -1,
			})

			code := r.URL.Query().Get("code")
			if code == "" {
				http.Error(w, "Missing code parameter", http.StatusBadRequest)
				return
			}

			tokenResp, err := oidc.ExchangeCode(code)
			if err != nil {
				http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			userInfo, err := oidc.GetUserInfo(tokenResp.AccessToken)
			if err != nil {
				http.Error(w, "Failed to fetch user info: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Extract username
			username, ok := userInfo["preferred_username"].(string)
			if !ok || username == "" {
				username, _ = userInfo["email"].(string)
			}
			if username == "" {
				username = "oidc-user"
			}

			// Generate local JWT token
			claims := auth.JWTClaims{
				Username:  username,
				Role:      "oidc-user",
				ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
				Issuer:    "servstore",
			}
			jwtToken, err := auth.GenerateToken(claims, wc.auth.JWTSecret())
			if err != nil {
				http.Error(w, "Failed to generate token", http.StatusInternalServerError)
				return
			}

			// Set token cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "token",
				Value:    jwtToken,
				Path:     "/",
				HttpOnly: true,
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   86400,
			})

			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		http.Error(w, "OIDC not configured", http.StatusBadRequest)
		return
	}

	if path == "/console/logout" {
		http.SetCookie(w, &http.Cookie{
			Name:     "token",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
		http.Redirect(w, r, "/login.html", http.StatusFound)
		return
	}

	// 2. Validate session/auth for console access
	if wc.auth.IsEnabled() {
		// Check if authenticated
		authenticated := wc.auth.VerifyRequest(r)

		// Allow public access to login page and public assets
		isPublicAsset := false
		if path == "/login.html" || path == "/style.css" || path == "/favicon.ico" {
			isPublicAsset = true
		}

		if !authenticated && !isPublicAsset {
			if strings.Contains(accept, "text/html") || path == "/" {
				http.Redirect(w, r, "/login.html", http.StatusFound)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Prevent authenticated users from going to login page
		if authenticated && path == "/login.html" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	// 2.5 Handle protected console endpoints
	if strings.HasPrefix(path, "/console/users/") && strings.HasSuffix(path, "/policy") {
		parts := strings.Split(path, "/")
		if len(parts) == 5 {
			username := parts[3]
			switch r.Method {
			case http.MethodGet:
				data, err := wc.store.GetUserPolicy(r.Context(), username)
				if err != nil {
					if os.IsNotExist(err) {
						w.Header().Set("Content-Type", "application/json")
						w.Write([]byte(`{"Statement":[]}`))
						return
					}
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
				return
			case http.MethodPut:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "Bad Request", http.StatusBadRequest)
					return
				}
				var p auth.Policy
				if err := json.Unmarshal(body, &p); err != nil {
					http.Error(w, "Invalid policy JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
				if err := wc.store.PutUserPolicy(r.Context(), username, body); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				return
			case http.MethodDelete:
				if err := wc.store.DeleteUserPolicy(r.Context(), username); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	}

	// 3. Serve standard assets or proxy to S3 Gateway
	isAsset := false
	if path == "/style.css" || path == "/app.js" || path == "/favicon.ico" || path == "/login.html" {
		isAsset = true
	} else if path == "/" && strings.Contains(accept, "text/html") {
		r.URL.Path = "/index.html"
		isAsset = true
	}

	if isAsset {
		wc.fileServer.ServeHTTP(w, r)
		return
	}

	wc.gateway.ServeHTTP(w, r)
}
