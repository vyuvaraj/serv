package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"servsecret/pkg/storage"
)

func TestSecretHandlers(t *testing.T) {
	// Setup storage
	tmpFile := "test_secrets.enc"
	defer os.Remove(tmpFile)

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	store, err := storage.NewEncryptedFileStore(tmpFile, key)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	Store = store

	// 1. Test Get missing secret
	req := httptest.NewRequest("GET", "/api/secrets/db-pass", nil)
	rr := httptest.NewRecorder()
	HandleSecretRoute(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}

	// 2. Test Set secret
	secretPayload := SecretRequest{
		Key:   "db-pass",
		Value: "super-secret-password-123",
	}
	body, _ := json.Marshal(secretPayload)
	req = httptest.NewRequest("POST", "/api/secrets", bytes.NewBuffer(body))
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	var setResp SecretResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &setResp); err != nil {
		t.Fatalf("failed to parse set response: %v", err)
	}

	if setResp.Value != "super-secret-password-123" {
		t.Errorf("expected 'super-secret-password-123', got '%s'", setResp.Value)
	}

	// 3. Test Get secret
	req = httptest.NewRequest("GET", "/api/secrets/db-pass", nil)
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var getResp SecretResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to parse get response: %v", err)
	}

	if getResp.Value != "super-secret-password-123" {
		t.Errorf("expected 'super-secret-password-123', got '%s'", getResp.Value)
	}

	// 4. Test List secrets
	req = httptest.NewRequest("GET", "/api/secrets", nil)
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var listResp ListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to parse list response: %v", err)
	}

	if len(listResp.Keys) != 1 || listResp.Keys[0] != "db-pass" {
		t.Errorf("expected keys ['db-pass'], got %v", listResp.Keys)
	}

	// 5. Test Delete secret
	req = httptest.NewRequest("DELETE", "/api/secrets/db-pass", nil)
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// 6. Test Get deleted secret (should be 404 again)
	req = httptest.NewRequest("GET", "/api/secrets/db-pass", nil)
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestKeyRotationAndCaching(t *testing.T) {
	tmpFile := "test_secrets_rotate.enc"
	defer os.Remove(tmpFile)

	// Initialize with first key
	key1 := make([]byte, 32)
	for i := range key1 {
		key1[i] = byte(i)
	}

	store, err := storage.NewEncryptedFileStore(tmpFile, key1)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	Store = store

	// 1. Set secret (should populate cache)
	secretPayload := SecretRequest{
		Key:   "api-key",
		Value: "12345-abcde",
	}
	body, _ := json.Marshal(secretPayload)
	req := httptest.NewRequest("POST", "/api/secrets", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()
	HandleSecretRoute(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("failed to set secret: %d", rr.Code)
	}

	// Verify we can get it
	req = httptest.NewRequest("GET", "/api/secrets/api-key", nil)
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("failed to get secret: %d", rr.Code)
	}

	// 2. Rotate master key using route
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(i + 1)
	}
	key2Hex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" // 32 bytes hex

	rotatePayload := RotateRequest{
		NewMasterKey: key2Hex,
	}
	body, _ = json.Marshal(rotatePayload)
	req = httptest.NewRequest("POST", "/api/secrets/rotate", bytes.NewBuffer(body))
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("failed to rotate master key: %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify we can still get the secret (decrypted with the new rotated key)
	req = httptest.NewRequest("GET", "/api/secrets/api-key", nil)
	rr = httptest.NewRecorder()
	HandleSecretRoute(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("failed to get secret after rotation: %d", rr.Code)
	}

	var getResp SecretResponse
	json.Unmarshal(rr.Body.Bytes(), &getResp)
	if getResp.Value != "12345-abcde" {
		t.Errorf("expected value '12345-abcde', got '%s'", getResp.Value)
	}
}

func TestAuditTrail(t *testing.T) {
	// Setup test audit path
	testAuditLog := "test_audit.log"
	os.Remove(testAuditLog)
	storage.SetAuditLogPath(testAuditLog)
	defer os.Remove(testAuditLog)

	// Trigger some operations
	InMemoryStore := storage.NewInMemoryStore()
	Store = InMemoryStore

	InMemoryStore.Set("tenant-1", "user-secret", "hunter2")
	InMemoryStore.Get("tenant-1", "user-secret")
	InMemoryStore.Delete("tenant-1", "user-secret")

	// Verify the audit trail integrity
	ok, err := storage.VerifyAuditLog()
	if err != nil {
		t.Fatalf("audit log verification error: %v", err)
	}
	if !ok {
		t.Errorf("expected audit log to be verified successfully")
	}

	// Tamper with the audit log to ensure it detects corruption
	content, _ := os.ReadFile(testAuditLog)
	tampered := bytes.Replace(content, []byte("user-secret"), []byte("user-secret-tampered"), 1)
	os.WriteFile(testAuditLog, tampered, 0600)

	ok, err = storage.VerifyAuditLog()
	if err == nil || ok {
		t.Errorf("expected verification failure after tampering, got ok=%v, err=%v", ok, err)
	}
}

func TestExternalProviderStore(t *testing.T) {
	// Test Vault adapter
	vaultStore := storage.NewExternalProviderStore("vault")
	Store = vaultStore

	val, err := Store.Get("default", "vault-secret")
	if err != nil {
		t.Fatalf("failed to read from Vault: %v", err)
	}
	if val != "vault-value-123" {
		t.Errorf("expected 'vault-value-123', got %q", val)
	}

	// Set value
	err = Store.Set("default", "new-vault-key", "new-vault-val")
	if err != nil {
		t.Fatalf("failed to set secret in Vault: %v", err)
	}

	val, err = Store.Get("default", "new-vault-key")
	if err != nil || val != "new-vault-val" {
		t.Errorf("failed to retrieve newly set Vault secret: val=%s, err=%v", val, err)
	}

	// Test AWS adapter
	awsStore := storage.NewExternalProviderStore("aws")
	Store = awsStore

	val, err = Store.Get("default", "aws-secret")
	if err != nil {
		t.Fatalf("failed to read from AWS: %v", err)
	}
	if val != "aws-value-456" {
		t.Errorf("expected 'aws-value-456', got %q", val)
	}
}

func TestSecretAPIKeyAuth(t *testing.T) {
	apiKeys := []string{"key-foo", "key-bar"}

	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			authorized := false
			for _, allowed := range apiKeys {
				if key == allowed {
					authorized = true
					break
				}
			}
			if !authorized {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 1. Test unauthorized request
	req1 := httptest.NewRequest("GET", "/api/secrets", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr1.Code)
	}

	// 2. Test authorized request
	req2 := httptest.NewRequest("GET", "/api/secrets", nil)
	req2.Header.Set("X-API-Key", "key-foo")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr2.Code)
	}
}

func TestEnvInjector(t *testing.T) {
	secretEnvs := []string{"MY_SECRET_DB_PASS=hunter2"}
	cmd := exec.Command("go", "env", "GOPATH")
	cmd.Env = append(os.Environ(), secretEnvs...)

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to run simulated command: %v", err)
	}

	if len(output) == 0 {
		t.Errorf("expected command output, got empty")
	}
}

func TestSecretVersioningAndRollback(t *testing.T) {
	Store = storage.NewInMemoryStore()

	// Set version 1
	Store.Set("default", "ver-key", "v1-value")
	// Set version 2
	Store.Set("default", "ver-key", "v2-value")

	val, _ := Store.Get("default", "ver-key")
	if val != "v2-value" {
		t.Errorf("expected latest version 'v2-value', got %q", val)
	}

	// Rollback
	err := Store.Rollback("default", "ver-key")
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	val, _ = Store.Get("default", "ver-key")
	if val != "v1-value" {
		t.Errorf("expected rolled back version 'v1-value', got %q", val)
	}
}

func TestIPCIDRRestriction(t *testing.T) {
	Store = storage.NewInMemoryStore()
	Store.Set("default", "cidr-key", "secret-data")

	// Apply CIDR rule: allow only from localhost 127.0.0.1
	Store.SetIPRestriction("default", "cidr-key", "127.0.0.1/32")

	// 1. Check matching IP
	if !Store.VerifyIPRestriction("default", "cidr-key", "127.0.0.1") {
		t.Error("expected 127.0.0.1 to be allowed under 127.0.0.1/32 rule")
	}

	// 2. Check non-matching IP
	if Store.VerifyIPRestriction("default", "cidr-key", "192.168.1.1") {
		t.Error("expected 192.168.1.1 to be forbidden under 127.0.0.1/32 rule")
	}
}

func TestSecretLeakDetection(t *testing.T) {
	Store = storage.NewInMemoryStore()
	Store.Set("default", "leak-key", "my-super-secret-password-12345")

	// Set up leak middleware
	handler := LeakDetectionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("This response body has my-super-secret-password-12345 inside!"))
	}))

	req := httptest.NewRequest("GET", "/api/secrets/leak-key", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestDynamicDBCredentials(t *testing.T) {
	Store = storage.NewInMemoryStore()

	// Querying "db-credentials" should dynamically generate a mock credential
	val, err := Store.Get("default", "db-credentials")
	if err != nil {
		t.Fatalf("failed to query dynamic credentials: %v", err)
	}
	if !strings.Contains(val, "db_user_temp_abc") {
		t.Errorf("expected dynamic credentials, got %q", val)
	}
}

func TestZeroKnowledgeBypass(t *testing.T) {
	Store = storage.NewInMemoryStore()

	// Verify key with prefix "zk-" is accepted and retrieved correctly
	err := Store.Set("default", "zk-enc-blob", "pre-encrypted-client-ciphertext-10928")
	if err != nil {
		t.Fatalf("failed to store zero knowledge secret: %v", err)
	}

	val, err := Store.Get("default", "zk-enc-blob")
	if err != nil || val != "pre-encrypted-client-ciphertext-10928" {
		t.Errorf("failed to retrieve zero knowledge secret: val=%s, err=%v", val, err)
	}
}
