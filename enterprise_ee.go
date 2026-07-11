//go:build enterprise

package main

import (
	"net/http"

	pkgdashboards "servconsole/pkg/dashboards"
	"servconsole/pkg/incidents"
	"servconsole/pkg/provision"
	"servconsole/pkg/tabs"
)

var (
	handleCostEstimation = pkgdashboards.HandleCostEstimation
	handleSLO            = pkgdashboards.HandleSLO
	handleRunbooks       = tabs.HandleRunbooks
	handleExecuteRunbook = tabs.HandleExecuteRunbook
	handleDashboards     = pkgdashboards.HandleDashboards
	handleProvisionStore = provision.HandleProvisionStore
	handleProvisionQueue = provision.HandleProvisionQueue
	handleDiagnosticExec = tabs.HandleDiagnosticExec
	handleEnvironments   = tabs.HandleEnvironments
	handleSelectEnvironment = tabs.HandleSelectEnvironment
	handleRollback       = tabs.HandleRollback
	handleTenantSwitch   = tabs.HandleTenantSwitch
)

// registerEnterpriseHandlers registers all enterprise-only endpoints in the EE build.
func registerEnterpriseHandlers(mux *http.ServeMux) {
	var sloTracker = incidents.NewSLOTracker()
	mux.HandleFunc("/api/cost-estimation", authorizeConsole(handleCostEstimation))
	mux.HandleFunc("/api/slo", authorizeConsole(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("decomposed") == "true" {
			sloTracker.HandleSLO(w, r)
		} else {
			handleSLO(w, r)
		}
	}))
	mux.HandleFunc("/api/runbooks", authorizeConsole(handleRunbooks))
	mux.HandleFunc("/api/runbooks/execute", authorizeConsole(handleExecuteRunbook))
	mux.HandleFunc("/api/dashboards", authorizeConsole(handleDashboards))
	mux.HandleFunc("/api/provision/store", authorizeConsole(handleProvisionStore))
	mux.HandleFunc("/api/provision/queue", authorizeConsole(handleProvisionQueue))
	mux.HandleFunc("/api/diagnostics/exec", authorizeConsole(handleDiagnosticExec))
	mux.HandleFunc("/api/environments", authorizeConsole(handleEnvironments))
	mux.HandleFunc("/api/environments/select", authorizeConsole(handleSelectEnvironment))
	mux.HandleFunc("/api/deployments/rollback", authorizeConsole(handleRollback))
	mux.HandleFunc("/api/tenant/switch", authorizeConsole(handleTenantSwitch))
}
