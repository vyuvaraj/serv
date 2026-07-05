//go:build !enterprise

package main

import "net/http"

// registerEnterpriseHandlers registers EE-gated endpoint stubs in the OSS build.
// These return 403 Forbidden directing users to the Enterprise Edition.
func registerEnterpriseHandlers(mux *http.ServeMux) {
	eeRequired := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSONError(w, r, "This feature requires ServConsole Enterprise Edition", "ERR_EE_REQUIRED", http.StatusForbidden)
	})

	mux.HandleFunc("/api/cost-estimation", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/slo", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/runbooks", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/runbooks/execute", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/dashboards", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/provision/store", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/provision/queue", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/diagnostics/exec", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/environments", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/environments/select", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/deployments/rollback", authorizeConsole(eeRequired))
	mux.HandleFunc("/api/tenant/switch", authorizeConsole(eeRequired))
}
