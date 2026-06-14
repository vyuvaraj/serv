package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// Simple credential store for MVP
type Credential struct {
	AccessKey string
	SecretKey string
}

type AuthProvider struct {
	credentials map[string]string // AccessKey -> SecretKey
	enabled     bool
}

func NewAuthProvider(accessKey, secretKey string, enabled bool) *AuthProvider {
	creds := make(map[string]string)
	if accessKey != "" && secretKey != "" {
		creds[accessKey] = secretKey
	}
	return &AuthProvider{
		credentials: creds,
		enabled:     enabled,
	}
}

func (ap *AuthProvider) IsEnabled() bool {
	return ap.enabled
}

// VerifyRequest validates the AWS Signature V4 of the incoming HTTP request.
func (ap *AuthProvider) VerifyRequest(r *http.Request) bool {
	if !ap.enabled {
		return true
	}

	authHeader := r.Header.Get("Authorization")
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
	// Format: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-date, Signature=...
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
	// 1. HTTP Method
	method := r.Method

	// 2. Canonical URI
	uri := r.URL.Path
	if uri == "" {
		uri = "/"
	}

	// 3. Canonical Query String
	// Need to sort query parameters
	queryParams := r.URL.Query()
	var queryKeys []string
	for k := range queryParams {
		if k == "X-Amz-Signature" || k == "Signature" {
			continue // Skip signature parameter itself in canonical string
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

	// 4. Canonical Headers
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

	// 5. Payload Hash
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

	// String to Sign
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

	// Calculate Signature
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStr))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	calculatedSignature := hexEncode(hmacSHA256(kSigning, []byte(stringToSign)))

	return hmac.Equal([]byte(calculatedSignature), []byte(signaturePart))
}

func (ap *AuthProvider) verifyQuerySignature(r *http.Request) bool {
	// Query params authentication
	algorithm := r.URL.Query().Get("X-Amz-Algorithm")
	if algorithm != "AWS4-HMAC-SHA256" {
		return false
	}

	credential := r.URL.Query().Get("X-Amz-Credential")
	signedHeaders := r.URL.Query().Get("X-Amz-SignedHeaders")
	signature := r.URL.Query().Get("X-Amz-Signature")
	date := r.URL.Query().Get("X-Amz-Date")

	if credential == "" || signedHeaders == "" || signature == "" || date == "" {
		return false
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

	// Canonical Request construction for query auth
	method := r.Method
	uri := r.URL.Path
	if uri == "" {
		uri = "/"
	}

	// Gather query keys excluding X-Amz-Signature
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

	// Canonical Headers
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

// pathEscape escapes string for S3 canonical URI/Query matching AWS SigV4 specification
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
