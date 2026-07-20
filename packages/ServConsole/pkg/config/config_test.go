package config

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLoadDiscoveryDefaults(t *testing.T) {
	os.Setenv("SERVVERSE_DISCOVERY", `{"gate":"http://gateway:8080"}`)
	defer os.Unsetenv("SERVVERSE_DISCOVERY")

	d := LoadDiscovery()
	if d.Gate != "http://gateway:8080" {
		t.Errorf("expected gate url, got %q", d.Gate)
	}
}

func TestHandleStatusEndpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	HandleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed decoding: %v", err)
	}
	if resp["components"] == nil {
		t.Errorf("expected components field in response, got nil")
	}
}

func TestComponentStatusCheck(t *testing.T) {
	status := CheckStatus("github.com/vyuvaraj/serv/packages/ServGate", "invalid-url-format")
	if status.Online {
		t.Error("expected offline status for invalid url")
	}
}
