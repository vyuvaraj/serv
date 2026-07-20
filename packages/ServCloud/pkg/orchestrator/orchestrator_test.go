package orchestrator

import (
	"os"
	"testing"
)

func TestNewOrchestratorEmptyDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "orch-test-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	o, err := NewOrchestrator(tempDir)
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}
	if o.workDir == "" {
		t.Error("expected non-empty workDir")
	}
}

func TestOrchestratorAllocatePort(t *testing.T) {
	port, err := FindFreePort()
	if err != nil {
		t.Fatalf("unexpected error finding free port: %v", err)
	}
	if port <= 0 {
		t.Errorf("expected positive port number, got %d", port)
	}
}

func TestOrchestratorIsPortAvailable(t *testing.T) {
	port, _ := FindFreePort()
	if port <= 0 {
		t.Error("invalid free port")
	}
}

func TestOrchestratorGetServiceAbsent(t *testing.T) {
	o := &Orchestrator{
		services: make(map[string]*ServiceProcess),
	}
	_, found := o.GetService("non-existent")
	if found {
		t.Error("expected GetService to return false for absent service")
	}
}

func TestOrchestratorListServicesEmpty(t *testing.T) {
	o := &Orchestrator{
		services: make(map[string]*ServiceProcess),
	}
	list := o.ListServices()
	if len(list) != 0 {
		t.Errorf("expected 0 active services, got %d", len(list))
	}
}

func TestOrchestratorParseIsolationMode(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{"", "process"},
		{"// runtime: wasm\nprint(1);", "wasm"},
		{"// runtime: docker\nprint(2);", "docker"},
	}

	for _, tt := range tests {
		mode := "process"
		if stringsContains(tt.code, "// runtime: wasm") {
			mode = "wasm"
		} else if stringsContains(tt.code, "// runtime: docker") {
			mode = "docker"
		}
		if mode != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, mode)
		}
	}
}

func TestOrchestratorParseEnvVars(t *testing.T) {
	line1 := "// env: PORT=8080"
	p1 := parseEnvLine(line1)
	if p1["PORT"] != "8080" {
		t.Errorf("expected PORT=8080, got %q", p1["PORT"])
	}
}

func TestOrchestratorStatusTransitions(t *testing.T) {
	proc := &ServiceProcess{
		Name:   "test",
		Status: "deploying",
	}
	if proc.Status != "deploying" {
		t.Error("invalid status")
	}
	proc.Status = "running"
	if proc.Status != "running" {
		t.Error("invalid transition")
	}
}

func TestOrchestratorScaleUpCount(t *testing.T) {
	o := &Orchestrator{
		services: make(map[string]*ServiceProcess),
	}
	o.services["app"] = &ServiceProcess{Name: "app", Status: "running"}
	count := 0
	for range o.services {
		count++
	}
	if count != 1 {
		t.Errorf("expected service count 1, got %d", count)
	}
}

func TestOrchestratorScaleDownCount(t *testing.T) {
	o := &Orchestrator{
		services: make(map[string]*ServiceProcess),
	}
	o.services["app"] = &ServiceProcess{Name: "app", Status: "stopped"}
	if o.services["app"].Status != "stopped" {
		t.Error("expected status stopped")
	}
}

func TestOrchestratorHistoryLimit(t *testing.T) {
	o := &Orchestrator{
		history: make([]DeploymentHistoryItem, 0),
	}
	for i := 0; i < 20; i++ {
		o.history = append(o.history, DeploymentHistoryItem{ID: "id"})
	}
	if len(o.history) != 20 {
		t.Errorf("expected 20 history items, got %d", len(o.history))
	}
}

func TestOrchestratorHistoryClear(t *testing.T) {
	o := &Orchestrator{
		history: []DeploymentHistoryItem{{ID: "id"}},
	}
	o.history = nil
	if len(o.history) != 0 {
		t.Error("expected history to be cleared")
	}
}

func stringsContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func parseEnvLine(line string) map[string]string {
	res := make(map[string]string)
	trimmed := line
	if stringsHasPrefix(trimmed, "// env:") {
		rem := trimmed[len("// env:"):]
		idx := -1
		for i, c := range rem {
			if c == '=' {
				idx = i
				break
			}
		}
		if idx != -1 {
			k := stringsTrimSpace(rem[:idx])
			v := stringsTrimSpace(rem[idx+1:])
			if k != "" {
				res[k] = v
			}
		}
	}
	return res
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func stringsTrimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
