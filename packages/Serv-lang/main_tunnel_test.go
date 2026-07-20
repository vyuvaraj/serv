package main

import (
	"testing"
)

func TestGitBranchSubdomainForServ(t *testing.T) {
	sub := getGitBranchSubdomainForServ()
	// Should resolve to the current branch name (usually main or similar)
	if sub == "" {
		t.Log("Git branch auto-detected as empty (could be non-git env)")
	} else {
		t.Logf("Git branch auto-detected as: %s", sub)
		// Ensure only valid characters are present in the resolved subdomain
		for _, char := range sub {
			if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
				t.Errorf("Invalid character %q found in resolved subdomain %q", char, sub)
			}
		}
	}
}
