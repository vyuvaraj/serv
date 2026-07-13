package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"servconsole/pkg/config"
)

var (
	OidcIssuer       = os.Getenv("SERV_OIDC_ISSUER")
	OidcClientID     = os.Getenv("SERV_OIDC_CLIENT_ID")
	OidcClientSecret = os.Getenv("SERV_OIDC_CLIENT_SECRET")
	OidcRedirectURL  = os.Getenv("SERV_OIDC_REDIRECT_URL")

	OidcAuthURL  string
	OidcTokenURL string
	JwtSecBytes  []byte

	EnterpriseRegisterSession = func(token string, username string) error { return nil }
	EnterpriseVerifySession   = func(token string) bool { return true }
	EnterpriseRevokeSession   = func(token string) error { return nil }

	AddAuditLog func(user string, action string, method string, path string, status int)
)

func Init(auditLogFunc func(string, string, string, string, int)) {
	AddAuditLog = auditLogFunc

	sec := config.ActiveDiscovery.JWTSecret
	if sec == "" {
		sec = fmt.Sprintf("ephemeral-%d-%d", time.Now().UnixNano(), sha256.Sum256([]byte("servconsole-salt")))
		log.Println("[auth] SERV_JWT_SECRET not set. Generated ephemeral session key.")
	}
	JwtSecBytes = []byte(sec)

	if OidcIssuer == "" {
		return
	}
	log.Printf("[OIDC] Issuer configured: %s", OidcIssuer)
	wellKnown := strings.TrimSuffix(OidcIssuer, "/") + "/.well-known/openid-configuration"
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(wellKnown)
	if err == nil {
		defer resp.Body.Close()
		var oidcCfg struct {
			AuthorizationEndpoint string `json:"authorization_endpoint"`
			TokenEndpoint         string `json:"token_endpoint"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&oidcCfg); err == nil {
			OidcAuthURL = oidcCfg.AuthorizationEndpoint
			OidcTokenURL = oidcCfg.TokenEndpoint
			log.Printf("[OIDC] Discovered endpoints: auth=%s, token=%s", OidcAuthURL, OidcTokenURL)
			return
		}
	}

	OidcAuthURL = strings.TrimSuffix(OidcIssuer, "/") + "/protocol/openid-connect/auth"
	OidcTokenURL = strings.TrimSuffix(OidcIssuer, "/") + "/protocol/openid-connect/token"
	log.Printf("[OIDC] Discovery failed or skipped. Using default endpoints: auth=%s, token=%s", OidcAuthURL, OidcTokenURL)
}

func AuthorizeConsole(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jwtSec := os.Getenv("SERV_JWT_SECRET")
		if jwtSec == "" && OidcIssuer == "" {
			next(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			if cookie, err := r.Cookie("token"); err == nil {
				authHeader = "Bearer " + cookie.Value
			}
		}

		if authHeader == "" {
			http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		username, role, ok := ValidateJWT(token, JwtSecBytes)
		if !ok || !EnterpriseVerifySession(token) {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		r.Header.Set("X-Console-User", username)
		r.Header.Set("X-Console-Role", role)
		next(w, r)
	}
}

func ValidateJWT(tokenStr string, secret []byte) (string, string, bool) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", "", false
	}

	headerPart, payloadPart, signaturePart := parts[0], parts[1], parts[2]

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerPart + "." + payloadPart))
	expectedMac := mac.Sum(nil)

	sigBytes, err := Base64UrlDecode(signaturePart)
	if err != nil || !hmac.Equal(sigBytes, expectedMac) {
		return "", "", false
	}

	payloadBytes, err := Base64UrlDecode(payloadPart)
	if err != nil {
		return "", "", false
	}

	var claims struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		Exp      int64  `json:"exp"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", "", false
	}

	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return "", "", false
	}

	role := claims.Role
	if role == "" {
		switch claims.Username {
		case "admin":
			role = "admin"
		case "operator", "developer-bob":
			role = "operator"
		default:
			role = "viewer"
		}
	}

	return claims.Username, role, true
}

func Base64UrlDecode(s string) ([]byte, error) {
	if l := len(s) % 4; l > 0 {
		s += strings.Repeat("=", 4-l)
	}
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	return base64.StdEncoding.DecodeString(s)
}

func GenerateLocalJWT(username string) (string, error) {
	role := "viewer"
	switch username {
	case "admin":
		role = "admin"
	case "operator", "developer-bob":
		role = "operator"
	}
	header := Base64UrlEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := Base64UrlEncode(fmt.Appendf(nil, `{"username":%q,"exp":%d,"role":%q}`, username, time.Now().Add(24*time.Hour).Unix(), role))

	mac := hmac.New(sha256.New, JwtSecBytes)
	mac.Write([]byte(header + "." + payload))
	signature := Base64UrlEncode(mac.Sum(nil))

	return header + "." + payload + "." + signature, nil
}

func Base64UrlEncode(b []byte) string {
	s := base64.URLEncoding.EncodeToString(b)
	return strings.TrimRight(s, "=")
}

func HandleAuthConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"sso_enabled": OidcIssuer != "",
		"issuer":      OidcIssuer,
		"client_id":   OidcClientID,
	})
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if OidcIssuer == "" {
		http.Error(w, "OIDC SSO is not configured", http.StatusBadRequest)
		return
	}

	u, err := url.Parse(OidcAuthURL)
	if err != nil {
		http.Error(w, "Invalid auth URL", http.StatusInternalServerError)
		return
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", OidcClientID)
	q.Set("redirect_uri", OidcRedirectURL)
	q.Set("scope", "openid profile email")
	q.Set("state", "random-state-string")
	u.RawQuery = q.Encode()

	http.Redirect(w, r, u.String(), http.StatusTemporaryRedirect)
}

func HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code query param", http.StatusBadRequest)
		return
	}

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", OidcRedirectURL)
	data.Set("client_id", OidcClientID)
	data.Set("client_secret", OidcClientSecret)

	req, err := http.NewRequest("POST", OidcTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		http.Error(w, "Failed to create token exchange request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Token exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("Token exchange returned status %d: %s", resp.StatusCode, string(body)), http.StatusInternalServerError)
		return
	}

	var res struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		http.Error(w, "Failed to decode token response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if res.IDToken == "" {
		http.Error(w, "No id_token returned", http.StatusInternalServerError)
		return
	}

	parts := strings.Split(res.IDToken, ".")
	if len(parts) != 3 {
		http.Error(w, "Invalid ID token format", http.StatusInternalServerError)
		return
	}
	payloadBytes, err := Base64UrlDecode(parts[1])
	if err != nil {
		http.Error(w, "Failed to decode ID token payload: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		Sub               string `json:"sub"`
	}
	_ = json.Unmarshal(payloadBytes, &claims)

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = claims.Sub
	}
	if username == "" {
		username = "sso-user"
	}

	localToken, err := GenerateLocalJWT(username)
	if err != nil {
		http.Error(w, "Failed to generate session token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = EnterpriseRegisterSession(localToken, username)

	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    localToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	if AddAuditLog != nil {
		AddAuditLog(username, "SSO Login Success", "GET", "/api/auth/callback", http.StatusOK)
	}
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func HandleLogout(w http.ResponseWriter, r *http.Request) {
	user := r.Header.Get("X-Console-User")
	if AddAuditLog != nil {
		AddAuditLog(user, "User Logged Out", "POST", "/api/auth/logout", http.StatusOK)
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		if cookie, err := r.Cookie("token"); err == nil {
			token = cookie.Value
		}
	}
	if token != "" {
		_ = EnterpriseRevokeSession(token)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"logged_out"}`))
}

func GetUserRole(r *http.Request) string {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		if cookie, err := r.Cookie("token"); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return "viewer"
	}
	_, role, ok := ValidateJWT(token, JwtSecBytes)
	if !ok {
		return "viewer"
	}
	return role
}

func HandleAuthMe(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		if cookie, err := r.Cookie("token"); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"username":"guest","role":"viewer"}`))
		return
	}

	username, role, ok := ValidateJWT(token, JwtSecBytes)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"username":"guest","role":"viewer"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"username": username,
		"role":     role,
	})
}
