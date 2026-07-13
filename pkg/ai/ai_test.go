package ai

import (
	"strings"
	"testing"
)

func TestGenerateIncidentResolutionSuggestion(t *testing.T) {
	// 1. Latency suggestion
	s1 := GenerateIncidentResolutionSuggestion("latency", "high database latency")
	if !strings.Contains(s1, "replica count") {
		t.Errorf("expected latency suggestions, got %q", s1)
	}

	// 2. Error suggestion
	s2 := GenerateIncidentResolutionSuggestion("error", "database connection failed")
	if !strings.Contains(s2, "audit logs") {
		t.Errorf("expected error suggestions, got %q", s2)
	}

	// 3. Fallback suggestion
	s3 := GenerateIncidentResolutionSuggestion("info", "everything normal")
	if !strings.Contains(s3, "health probes") {
		t.Errorf("expected fallback suggestions, got %q", s3)
	}
}
