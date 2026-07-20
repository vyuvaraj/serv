package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"testing"
)

func TestConfigureProxyDirector(t *testing.T) {
	target, _ := url.Parse("http://localhost:8080")
	rp := httputil.NewSingleHostReverseProxy(target)

	var logCalled bool
	addAuditLog := func(user, action, method, path string, status int) {
		logCalled = true
	}
	getAction := func(prefix, path string) string { return "test-action" }

	ConfigureProxyDirector(rp, target, "/api/proxy", "secret-token", getAction, addAuditLog)

	// Verify director modifications
	req, _ := http.NewRequest("GET", "http://console/api/proxy/v1/status", nil)
	rp.Director(req)

	if req.URL.Host != "localhost:8080" {
		t.Errorf("expected localhost:8080, got %s", req.URL.Host)
	}
	if req.Header.Get("Authorization") != "Bearer secret-token" {
		t.Errorf("token header missing")
	}

	// Verify modify response hook
	resp := &http.Response{
		Request:    req,
		StatusCode: http.StatusOK,
	}
	req.Method = "POST"
	_ = rp.ModifyResponse(resp)

	if !logCalled {
		t.Error("expected audit log to be called")
	}
}
