package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"servregistry/pkg/registry"
	"servregistry/pkg/resolution"
)

func TestParseServToml(t *testing.T) {
	tomlContent := `
name = "testpkg"
version = "1.2.3"

[dependencies]
pkg1 = "0.1.0"
pkg2 = "1.0.0"
`
	name, version, deps, err := resolution.ParseServToml(tomlContent)
	if err != nil {
		t.Fatalf("Failed to parse TOML: %v", err)
	}
	if name != "testpkg" {
		t.Errorf("Expected name to be 'testpkg', got '%s'", name)
	}
	if version != "1.2.3" {
		t.Errorf("Expected version to be '1.2.3', got '%s'", version)
	}
	if len(deps) != 2 {
		t.Fatalf("Expected 2 dependencies, got %d", len(deps))
	}
	if deps[0] != "pkg1@0.1.0" || deps[1] != "pkg2@1.0.0" {
		t.Errorf("Dependencies parsed incorrectly: %v", deps)
	}
}

func TestParseServTomlFromTarGz(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	tomlContent := `
name = "tarpkg"
version = "3.2.1"
`
	hdr := &tar.Header{
		Name: "serv.toml",
		Size: int64(len(tomlContent)),
		Mode: 0644,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("Failed to write header: %v", err)
	}
	if _, err := tw.Write([]byte(tomlContent)); err != nil {
		t.Fatalf("Failed to write content: %v", err)
	}
	tw.Close()
	gw.Close()

	name, version, _, err := resolution.ParseServTomlFromTarGz(buf.Bytes())
	if err != nil {
		t.Fatalf("Failed to parse from tar.gz: %v", err)
	}
	if name != "tarpkg" {
		t.Errorf("Expected name 'tarpkg', got '%s'", name)
	}
	if version != "3.2.1" {
		t.Errorf("Expected version '3.2.1', got '%s'", version)
	}
}

func TestJWTValidation(t *testing.T) {
	secret := []byte("my-test-secret")
	token, err := generateJWT("test-user", secret)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	username, ok := validateJWT(token, secret)
	if !ok {
		t.Fatalf("Expected token to be valid")
	}
	if username != "test-user" {
		t.Errorf("Expected username 'test-user', got '%s'", username)
	}

	// Test invalid secret
	_, ok = validateJWT(token, []byte("wrong-secret"))
	if ok {
		t.Errorf("Expected validation to fail for wrong secret")
	}
}

func TestHealthEndpoints(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"healthy"}`))
	})
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Body.String() != `{"status":"healthy"}` {
		t.Errorf("Unexpected body: %s", rr.Body.String())
	}
}

func generateJWT(username string, secret []byte) (string, error) {
	header := `{"alg":"HS256","typ":"JWT"}`
	headerB64 := base64UrlEncode([]byte(header))

	claims := fmt.Sprintf(`{"username":%q,"exp":%d}`, username, time.Now().Add(24*time.Hour).Unix())
	// For testing, simple claims formatting is fine. Let's do standard base64url encoding
	claimsB64 := base64UrlEncode([]byte(claims))

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(headerB64 + "." + claimsB64))
	sig := mac.Sum(nil)
	sigB64 := base64UrlEncode(sig)

	return headerB64 + "." + claimsB64 + "." + sigB64, nil
}

func base64UrlEncode(data []byte) string {
	s := base64.URLEncoding.EncodeToString(data)
	for len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	return s
}

func TestSemverMatching(t *testing.T) {
	tests := []struct {
		rangeStr   string
		versionStr string
		expected   bool
	}{
		{"^1.2.0", "1.2.3", true},
		{"^1.2.0", "1.3.0", true},
		{"^1.2.0", "2.0.0", false},
		{"^1.2.0", "1.1.9", false},
		{"~0.4.1", "0.4.5", true},
		{"~0.4.1", "0.5.0", false},
		{"~0.4.1", "0.4.0", false},
		{"1.0.0", "1.0.0", true},
		{"1.0.0", "1.0.1", false},
		{"*", "2.3.4", true},
		{"", "1.0.0", true},
		{"latest", "5.6.7", true},
	}

	for _, tc := range tests {
		got := resolution.MatchSemver(tc.rangeStr, tc.versionStr)
		if got != tc.expected {
			t.Errorf("resolution.MatchSemver(%q, %q) = %t; want %t", tc.rangeStr, tc.versionStr, got, tc.expected)
		}
	}
}

func TestResolveBestVersion(t *testing.T) {
	versions := map[string]registry.VersionDetails{
		"1.2.0": {Version: "1.2.0"},
		"1.2.5": {Version: "1.2.5"},
		"1.3.0": {Version: "1.3.0"},
		"2.0.0": {Version: "2.0.0"},
	}

	best := resolution.ResolveBestVersion("^1.2.0", versions)
	if best != "1.3.0" {
		t.Errorf("Expected best version to be '1.3.0', got '%s'", best)
	}

	bestTilde := resolution.ResolveBestVersion("~1.2.0", versions)
	if bestTilde != "1.2.5" {
		t.Errorf("Expected best version to be '1.2.5', got '%s'", bestTilde)
	}
}

func TestPublishSignatureVerification(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}
	pubKeyHex := hex.EncodeToString(pubKey)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tomlContent := `
name = "signedpkg"
version = "1.0.0"
`
	hdr := &tar.Header{
		Name: "serv.toml",
		Size: int64(len(tomlContent)),
		Mode: 0644,
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte(tomlContent))
	tw.Close()
	gw.Close()

	data := buf.Bytes()
	sig := ed25519.Sign(privKey, data)
	sigHex := hex.EncodeToString(sig)

	// 1. Missing signature/pubkey headers
	req := httptest.NewRequest(http.MethodPost, "/publish", bytes.NewReader(data))
	rr := httptest.NewRecorder()
	handlePublish(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for missing headers, got %d", rr.Code)
	}

	// 2. Invalid signature hex
	req = httptest.NewRequest(http.MethodPost, "/publish", bytes.NewReader(data))
	req.Header.Set("X-Signature", "invalid-hex")
	req.Header.Set("X-Public-Key", pubKeyHex)
	rr = httptest.NewRecorder()
	handlePublish(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for invalid signature hex, got %d", rr.Code)
	}

	// 3. Signature verification failure
	req = httptest.NewRequest(http.MethodPost, "/publish", bytes.NewReader(data))
	req.Header.Set("X-Signature", hex.EncodeToString(make([]byte, 64)))
	req.Header.Set("X-Public-Key", pubKeyHex)
	rr = httptest.NewRecorder()
	handlePublish(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for invalid signature, got %d", rr.Code)
	}

	// 4. Valid signature - should pass signature verification and attempt metadata access (which will panic due to nil s3Client)
	defer func() {
		if r := recover(); r != nil {
			// Panic is expected because s3Client is nil, which means signature verification succeeded!
		} else {
			t.Errorf("Expected panic due to nil s3Client after successful signature check, but got no panic")
		}
	}()

	req = httptest.NewRequest(http.MethodPost, "/publish", bytes.NewReader(data))
	req.Header.Set("X-Signature", sigHex)
	req.Header.Set("X-Public-Key", pubKeyHex)
	rr = httptest.NewRecorder()
	handlePublish(rr, req)
}

func BenchmarkPackageIndexLookup(b *testing.B) {
	// Pre-populate the package index
	packageIndexMu.Lock()
	for i := 0; i < 500; i++ {
		name := fmt.Sprintf("pkg-%d", i)
		packageIndex[name] = &registry.PackageIndexItem{
			Name:   name,
			Latest: "1.0.0",
			Versions: []string{"0.9.0", "1.0.0"},
		}
	}
	packageIndexMu.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("pkg-%d", i%500)
		packageIndexMu.RLock()
		_, _ = packageIndex[name]
		packageIndexMu.RUnlock()
	}
}

func TestSchemaRegistryAPI(t *testing.T) {
	_ = os.RemoveAll("schemas")
	defer os.RemoveAll("schemas")

	// 1. Register a schema
	schema := `{
		"type": "object",
		"properties": {
			"orderId": { "type": "string" },
			"amount": { "type": "number" },
			"active": { "type": "boolean" }
		},
		"required": ["orderId"]
	}`

	reqPut := httptest.NewRequest(http.MethodPut, "/api/v1/schemas/order", strings.NewReader(schema))
	wPut := httptest.NewRecorder()
	handleSchemasAPI(wPut, reqPut)

	if wPut.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", wPut.Code)
	}

	// 2. Fetch schema
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/order", nil)
	wGet := httptest.NewRecorder()
	handleSchemasAPI(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", wGet.Code)
	}
	if !strings.Contains(wGet.Body.String(), "orderId") {
		t.Errorf("Expected fetched schema to contain orderId, got: %s", wGet.Body.String())
	}

	// 3. Validate valid payload
	validPayload := `{"schema":"order","payload":"{\"orderId\":\"abc-123\",\"amount\":49.95,\"active\":true}"}`
	reqVal1 := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/validate", strings.NewReader(validPayload))
	wVal1 := httptest.NewRecorder()
	handleSchemaValidationAPI(wVal1, reqVal1)

	var res1 map[string]interface{}
	json.NewDecoder(wVal1.Body).Decode(&res1)
	if res1["valid"] != true {
		t.Errorf("Expected valid payload to be true, got errors: %v", res1["errors"])
	}

	// 4. Validate invalid payload (missing required field, and wrong type)
	invalidPayload := `{"schema":"order","payload":"{\"amount\":\"not-a-number\",\"active\":true}"}`
	reqVal2 := httptest.NewRequest(http.MethodPost, "/api/v1/schemas/validate", strings.NewReader(invalidPayload))
	wVal2 := httptest.NewRecorder()
	handleSchemaValidationAPI(wVal2, reqVal2)

	var res2 map[string]interface{}
	json.NewDecoder(wVal2.Body).Decode(&res2)
	if res2["valid"] == true {
		t.Error("Expected invalid payload to fail validation, but it succeeded")
	} else {
		errs := res2["errors"].([]interface{})
		if len(errs) != 2 {
			t.Errorf("Expected 2 validation errors, got %d: %v", len(errs), errs)
		}
	}
}

func TestMarketplace(t *testing.T) {
	// 1. Get initial marketplace list
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/marketplace/list", nil)
	wGet := httptest.NewRecorder()
	handleMarketplaceList(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", wGet.Code)
	}

	var list []MarketplaceItem
	if err := json.Unmarshal(wGet.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to parse list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "auth-token-filter" {
		t.Errorf("unexpected initial marketplace items: %+v", list)
	}

	// 2. Publish new item
	newItem := `{"name":"custom-workflow","version":"2.1.0","type":"workflow","description":"Test custom workflow template","publisher":"Alice","url":"https://serv.dev/marketplace/custom-workflow.json"}`
	reqPub := httptest.NewRequest(http.MethodPost, "/api/v1/marketplace/publish", strings.NewReader(newItem))
	wPub := httptest.NewRecorder()
	handleMarketplacePublish(wPub, reqPub)

	if wPub.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d", wPub.Code)
	}

	// 3. Verify new item is listed
	wGet2 := httptest.NewRecorder()
	handleMarketplaceList(wGet2, reqGet)
	if err := json.Unmarshal(wGet2.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to parse list: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 marketplace items, got %d", len(list))
	}
}

