//go:build !enterprise

package main

import (
	"net/http"
)

func handleAIRootCause(w http.ResponseWriter, r *http.Request) {
	WriteJSONError(w, r, "AI Root Cause Analysis requires Enterprise Edition", "ERR_ENTERPRISE_REQUIRED", http.StatusForbidden)
}
