package ServShared

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

type mockValidatable struct {
	User  string `json:"user"`
	Email string `json:"email"`
}

func (m *mockValidatable) Validate() error {
	return nil
}

func FuzzDecodeAndValidateJSON(f *testing.F) {
	f.Add([]byte(`{"user": "alice", "email": "alice@example.com"}`))
	f.Add([]byte(`{"user": "", "email": ""}`))
	f.Add([]byte(`invalid json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		req := httptest.NewRequest("POST", "/", bytes.NewReader(data))
		w := httptest.NewRecorder()
		var m mockValidatable
		DecodeAndValidateJSON(w, req, &m)
	})
}

func FuzzSanitizeLog(f *testing.F) {
	f.Add("password=secret123")
	f.Add("bearer token-value")
	f.Add("normal message")

	f.Fuzz(func(t *testing.T, data string) {
		SanitizeLog(data)
	})
}

func TestSanitizeLogQuoted(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"password=secret123", "password=[REDACTED]"},
		{`"password": "my-secret-123"`, `"password": "[REDACTED]"`},
		{`'token'='my-token'`, `'token'='[REDACTED]'`},
		{"normal log message", "normal log message"},
	}

	for _, tc := range tests {
		got := SanitizeLog(tc.input)
		if got != tc.expected {
			t.Errorf("SanitizeLog(%q) = %q; expected %q", tc.input, got, tc.expected)
		}
	}
}

func FuzzValidateToken(f *testing.F) {
	f.Add("invalid-token-string")
	f.Add("header.payload.signature")
	f.Add("")

	validator := NewAuthValidator("my-secret-key-12345", "", "")

	f.Fuzz(func(t *testing.T, tokenStr string) {
		_, _ = validator.ValidateToken(tokenStr)
	})
}

