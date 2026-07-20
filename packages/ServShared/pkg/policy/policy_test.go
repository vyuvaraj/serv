package policy

import (
	"testing"
)

func TestEvaluatePolicy(t *testing.T) {
	schemaJSON := []byte(`{
		"version": 1,
		"rules": [
			{
				"id": "rule-1",
				"action": "deny",
				"methods": ["DELETE"],
				"path": "/api/secure/*"
			},
			{
				"id": "rule-2",
				"action": "allow",
				"methods": ["GET", "POST"],
				"path": "/api/secure/*",
				"roles": ["admin"]
			},
			{
				"id": "rule-3",
				"action": "deny",
				"methods": ["*"],
				"path": "/api/secure/*"
			}
		]
	}`)

	schema, err := ParsePolicySchema(schemaJSON)
	if err != nil {
		t.Fatalf("failed to parse schema: %v", err)
	}

	// 1. DELETE /api/secure/data -> deny
	action, _, _, matched := EvaluatePolicy("DELETE", "/api/secure/data", nil, []string{"admin"}, schema)
	if !matched || action != "deny" {
		t.Errorf("expected deny, got action=%q, matched=%t", action, matched)
	}

	// 2. POST /api/secure/data with role 'admin' -> allow
	action, _, _, matched = EvaluatePolicy("POST", "/api/secure/data", nil, []string{"admin"}, schema)
	if !matched || action != "allow" {
		t.Errorf("expected allow, got action=%q, matched=%t", action, matched)
	}

	// 3. POST /api/secure/data with role 'viewer' -> deny (falls through to rule-3)
	action, _, _, matched = EvaluatePolicy("POST", "/api/secure/data", nil, []string{"viewer"}, schema)
	if !matched || action != "deny" {
		t.Errorf("expected deny, got action=%q, matched=%t", action, matched)
	}
}
