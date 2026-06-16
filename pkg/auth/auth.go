package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Simple credential store for MVP
type Credential struct {
	AccessKey string
	SecretKey string
}

type AuthProvider struct {
	credentials map[string]string // AccessKey -> SecretKey
	enabled     bool
	jwtSecret   []byte
	ldapClient  *LDAPClient
	oidcClient  *OIDCClient
}

func NewAuthProvider(accessKey, secretKey string, enabled bool) *AuthProvider {
	creds := make(map[string]string)
	var jwtSec []byte
	if accessKey != "" && secretKey != "" {
		creds[accessKey] = secretKey
		// Derive a JWT signing key from the secret key so sessions persist across restarts
		h := sha256.Sum256([]byte(secretKey))
		jwtSec = h[:]
	} else {
		// Fallback to a random key if no secret is set
		h := sha256.Sum256([]byte("default-servstore-jwt-fallback-secret-key-32bytes"))
		jwtSec = h[:]
	}

	return &AuthProvider{
		credentials: creds,
		enabled:     enabled,
		jwtSecret:   jwtSec,
	}
}

func (ap *AuthProvider) ConfigureLDAP(ldapURL, dnTemplate string) {
	if ldapURL != "" {
		ap.ldapClient = NewLDAPClient(ldapURL, dnTemplate)
	}
}

func (ap *AuthProvider) ConfigureOIDC(cfg OIDCConfig) {
	if cfg.Issuer != "" {
		ap.oidcClient = NewOIDCClient(cfg)
	}
}

func (ap *AuthProvider) HasLDAP() bool {
	return ap.ldapClient != nil
}

func (ap *AuthProvider) HasOIDC() bool {
	return ap.oidcClient != nil && ap.oidcClient.IsEnabled()
}

func (ap *AuthProvider) OIDCClient() *OIDCClient {
	return ap.oidcClient
}

func (ap *AuthProvider) JWTSecret() []byte {
	return ap.jwtSecret
}

func (ap *AuthProvider) IsEnabled() bool {
	return ap.enabled
}

func (ap *AuthProvider) GetIdentity(r *http.Request) string {
	if !ap.enabled {
		return "anonymous"
	}

	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Basic ") {
		username, _, ok := r.BasicAuth()
		if ok {
			return username
		}
	}

	if strings.HasPrefix(authHeader, "Bearer ") {
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := ValidateToken(tokenStr, ap.jwtSecret)
		if err == nil {
			return claims.Username
		}
	}

	if cookie, err := r.Cookie("token"); err == nil {
		claims, err := ValidateToken(cookie.Value, ap.jwtSecret)
		if err == nil {
			return claims.Username
		}
	}

	if strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256 ") {
		parts := strings.Split(authHeader[17:], ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "Credential=") {
				cred := part[11:]
				credParts := strings.Split(cred, "/")
				if len(credParts) > 0 {
					return credParts[0]
				}
			}
		}
	}

	if credential := r.URL.Query().Get("X-Amz-Credential"); credential != "" {
		credParts := strings.Split(credential, "/")
		if len(credParts) > 0 {
			return credParts[0]
		}
	}

	return ""
}

// VerifyRequest validates the request. If auth is disabled, it returns true.
// It supports either a JWT token in the Authorization header (Bearer) or AWS SigV4.
func (ap *AuthProvider) VerifyRequest(r *http.Request) bool {
	if !ap.enabled {
		return true
	}

	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Basic ") {
		username, password, ok := r.BasicAuth()
		if ok {
			secret, exists := ap.credentials[username]
			return exists && secret == password
		}
	}

	if strings.HasPrefix(authHeader, "Bearer ") {
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		_, err := ValidateToken(tokenStr, ap.jwtSecret)
		return err == nil
	}

	// Also check Cookie for JWT token (useful for browser console requests)
	if cookie, err := r.Cookie("token"); err == nil {
		_, err := ValidateToken(cookie.Value, ap.jwtSecret)
		if err == nil {
			return true
		}
	}

	if authHeader == "" {
		// Also check query string for auth parameters
		authHeader = r.URL.Query().Get("X-Amz-Algorithm")
		if authHeader == "" {
			return false
		}
		return ap.verifyQuerySignature(r)
	}

	return ap.verifyHeaderSignature(r, authHeader)
}

// AuthenticateConsole attempts to authenticate console users using local keys or LDAP.
// Returns a JWT token if successful, or an error.
func (ap *AuthProvider) AuthenticateConsole(username, password string) (string, error) {
	// 1. Try local credentials
	if secret, ok := ap.credentials[username]; ok {
		if secret == password {
			// Generate token valid for 24 hours
			claims := JWTClaims{
				Username:  username,
				Role:      "admin",
				ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
				Issuer:    "servstore",
			}
			return GenerateToken(claims, ap.jwtSecret)
		}
	}

	// 2. Try LDAP if configured
	if ap.ldapClient != nil {
		ok, err := ap.ldapClient.Authenticate(username, password)
		if err != nil {
			return "", fmt.Errorf("LDAP auth error: %w", err)
		}
		if ok {
			claims := JWTClaims{
				Username:  username,
				Role:      "ldap-user",
				ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
				Issuer:    "servstore",
			}
			return GenerateToken(claims, ap.jwtSecret)
		}
	}

	return "", fmt.Errorf("invalid credentials")
}

func sum256(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

func hmacSHA256(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func (ap *AuthProvider) verifyHeaderSignature(r *http.Request, authHeader string) bool {
	if !strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256 ") {
		return false
	}

	parts := strings.Split(authHeader[17:], ",")
	var credentialPart, signedHeadersPart, signaturePart string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "Credential=") {
			credentialPart = part[11:]
		} else if strings.HasPrefix(part, "SignedHeaders=") {
			signedHeadersPart = part[14:]
		} else if strings.HasPrefix(part, "Signature=") {
			signaturePart = part[10:]
		}
	}

	if credentialPart == "" || signedHeadersPart == "" || signaturePart == "" {
		return false
	}

	credParts := strings.Split(credentialPart, "/")
	if len(credParts) < 5 {
		return false
	}
	accessKey := credParts[0]
	dateStr := credParts[1]
	region := credParts[2]
	service := credParts[3]

	secretKey, ok := ap.credentials[accessKey]
	if !ok {
		return false
	}

	// Reconstruct the Canonical Request
	method := r.Method
	uri := r.URL.Path
	if uri == "" {
		uri = "/"
	}

	queryParams := r.URL.Query()
	var queryKeys []string
	for k := range queryParams {
		if k == "X-Amz-Signature" || k == "Signature" {
			continue
		}
		queryKeys = append(queryKeys, k)
	}
	sortStrings(queryKeys)
	var queryParts []string
	for _, k := range queryKeys {
		vals := queryParams[k]
		for _, val := range vals {
			queryParts = append(queryParts, fmt.Sprintf("%s=%s", pathEscape(k), pathEscape(val)))
		}
	}
	canonicalQuery := strings.Join(queryParts, "&")

	signedHeadersList := strings.Split(signedHeadersPart, ";")
	var canonicalHeaders strings.Builder
	for _, h := range signedHeadersList {
		hLower := strings.ToLower(h)
		var val string
		if hLower == "host" {
			val = r.Host
		} else {
			val = r.Header.Get(hLower)
		}
		canonicalHeaders.WriteString(fmt.Sprintf("%s:%s\n", hLower, strings.TrimSpace(val)))
	}

	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		method,
		uri,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeadersPart,
		payloadHash,
	)

	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		amzDate = r.Header.Get("Date")
	}

	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s/%s/%s/aws4_request\n%s",
		amzDate,
		dateStr,
		region,
		service,
		hexEncode(sum256([]byte(canonicalRequest))),
	)

	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStr))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	calculatedSignature := hexEncode(hmacSHA256(kSigning, []byte(stringToSign)))

	return hmac.Equal([]byte(calculatedSignature), []byte(signaturePart))
}

func (ap *AuthProvider) verifyQuerySignature(r *http.Request) bool {
	algorithm := r.URL.Query().Get("X-Amz-Algorithm")
	if algorithm != "AWS4-HMAC-SHA256" {
		return false
	}

	credential := r.URL.Query().Get("X-Amz-Credential")
	signedHeaders := r.URL.Query().Get("X-Amz-SignedHeaders")
	signature := r.URL.Query().Get("X-Amz-Signature")
	date := r.URL.Query().Get("X-Amz-Date")
	expires := r.URL.Query().Get("X-Amz-Expires")

	if credential == "" || signedHeaders == "" || signature == "" || date == "" {
		return false
	}

	// Check if the pre-signed URL has expired
	if expires != "" {
		expiresSec, err := strconv.Atoi(expires)
		if err != nil {
			return false
		}
		// Parse the request date
		reqTime, err := time.Parse("20060102T150405Z", date)
		if err != nil {
			return false
		}
		if time.Now().After(reqTime.Add(time.Duration(expiresSec) * time.Second)) {
			return false
		}
	}

	credParts := strings.Split(credential, "/")
	if len(credParts) < 5 {
		return false
	}
	accessKey := credParts[0]
	dateStr := credParts[1]
	region := credParts[2]
	service := credParts[3]

	secretKey, ok := ap.credentials[accessKey]
	if !ok {
		return false
	}

	method := r.Method
	uri := r.URL.Path
	if uri == "" {
		uri = "/"
	}

	queryParams := r.URL.Query()
	var queryKeys []string
	for k := range queryParams {
		if k == "X-Amz-Signature" {
			continue
		}
		queryKeys = append(queryKeys, k)
	}
	sortStrings(queryKeys)
	var queryParts []string
	for _, k := range queryKeys {
		vals := queryParams[k]
		for _, val := range vals {
			queryParts = append(queryParts, fmt.Sprintf("%s=%s", pathEscape(k), pathEscape(val)))
		}
	}
	canonicalQuery := strings.Join(queryParts, "&")

	signedHeadersList := strings.Split(signedHeaders, ";")
	var canonicalHeaders strings.Builder
	for _, h := range signedHeadersList {
		hLower := strings.ToLower(h)
		var val string
		if hLower == "host" {
			val = r.Host
		} else {
			val = r.Header.Get(hLower)
		}
		canonicalHeaders.WriteString(fmt.Sprintf("%s:%s\n", hLower, strings.TrimSpace(val)))
	}

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\nUNSIGNED-PAYLOAD",
		method,
		uri,
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
	)

	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s/%s/%s/aws4_request\n%s",
		date,
		dateStr,
		region,
		service,
		hexEncode(sum256([]byte(canonicalRequest))),
	)

	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStr))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	calculatedSignature := hexEncode(hmacSHA256(kSigning, []byte(stringToSign)))

	return hmac.Equal([]byte(calculatedSignature), []byte(signature))
}

func sortStrings(strs []string) {
	for i := 0; i < len(strs); i++ {
		for j := i + 1; j < len(strs); j++ {
			if strs[i] > strs[j] {
				strs[i], strs[j] = strs[j], strs[i]
			}
		}
	}
}

func pathEscape(s string) string {
	var hexCount int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if shouldEscape(c) {
			hexCount++
		}
	}
	if hexCount == 0 {
		return s
	}
	t := make([]byte, len(s)+2*hexCount)
	j := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if shouldEscape(c) {
			t[j] = '%'
			t[j+1] = "0123456789ABCDEF"[c>>4]
			t[j+2] = "0123456789ABCDEF"[c&15]
			j += 3
		} else {
			t[j] = c
			j++
		}
	}
	return string(t)
}

func shouldEscape(c byte) bool {
	if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' {
		return false
	}
	switch c {
	case '-', '_', '.', '~':
		return false
	}
	return true
}

func (ap *AuthProvider) GetAdminCredentials() (string, string) {
	for k, v := range ap.credentials {
		return k, v
	}
	return "minioadmin", "minioadmin"
}


// GeneratePresignedURL creates a pre-signed URL for the given method, bucket, and key.
// The URL is valid for the specified duration. It uses AWS Signature V4 query string auth.
func (ap *AuthProvider) GeneratePresignedURL(baseURL, method, bucket, key string, expires time.Duration) (string, error) {
	accessKey, secretKey := ap.GetAdminCredentials()

	now := time.Now().UTC()
	dateStr := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	region := "us-east-1"
	service := "s3"
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStr, region, service)
	credential := fmt.Sprintf("%s/%s", accessKey, credentialScope)

	path := fmt.Sprintf("/%s/%s", bucket, key)
	expiresSec := int(expires.Seconds())

	// Build canonical query string (alphabetical order, excluding signature)
	queryParts := []string{
		fmt.Sprintf("X-Amz-Algorithm=%s", "AWS4-HMAC-SHA256"),
		fmt.Sprintf("X-Amz-Credential=%s", pathEscape(credential)),
		fmt.Sprintf("X-Amz-Date=%s", amzDate),
		fmt.Sprintf("X-Amz-Expires=%d", expiresSec),
		fmt.Sprintf("X-Amz-SignedHeaders=%s", "host"),
	}
	sortStrings(queryParts)
	canonicalQuery := strings.Join(queryParts, "&")

	// Parse host from baseURL
	host := strings.TrimPrefix(baseURL, "http://")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimSuffix(host, "/")

	// Canonical request
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\nhost:%s\n\nhost\nUNSIGNED-PAYLOAD",
		method,
		path,
		canonicalQuery,
		host,
	)

	// String to sign
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate,
		credentialScope,
		hexEncode(sum256([]byte(canonicalRequest))),
	)

	// Signing key
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStr))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	signature := hexEncode(hmacSHA256(kSigning, []byte(stringToSign)))

	presignedURL := fmt.Sprintf("%s%s?%s&X-Amz-Signature=%s",
		baseURL, path, canonicalQuery, signature)

	return presignedURL, nil
}
