//go:build enterprise

package main

import (
	"net/http"

	pkgdashboards "servconsole/pkg/dashboards"
	"servconsole/pkg/incidents"
	"servconsole/pkg/provision"
	"servconsole/pkg/tabs"
)

// registerEnterpriseHandlers registers all enterprise-only endpoints in the EE build.
func registerEnterpriseHandlers(mux *http.ServeMux) {
	pkgdashboards.ActivePredictiveAlertsProvider = &eePredictiveAlertsProvider{}

	var sloTracker = incidents.NewSLOTracker()
	mux.HandleFunc("/api/cost-estimation", authorizeConsole(pkgdashboards.HandleCostEstimation))
	mux.HandleFunc("/api/slo", authorizeConsole(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("decomposed") == "true" {
			sloTracker.HandleSLO(w, r)
		} else {
			pkgdashboards.HandleSLO(w, r)
		}
	}))
	mux.HandleFunc("/api/runbooks", authorizeConsole(tabs.HandleRunbooks))
	mux.HandleFunc("/api/runbooks/execute", authorizeConsole(tabs.HandleExecuteRunbook))
	mux.HandleFunc("/api/dashboards", authorizeConsole(pkgdashboards.HandleDashboards))
	mux.HandleFunc("/api/provision/store", authorizeConsole(provision.HandleProvisionStore))
	mux.HandleFunc("/api/provision/queue", authorizeConsole(provision.HandleProvisionQueue))
	mux.HandleFunc("/api/diagnostics/exec", authorizeConsole(tabs.HandleDiagnosticExec))
	mux.HandleFunc("/api/environments", authorizeConsole(tabs.HandleEnvironments))
	mux.HandleFunc("/api/environments/select", authorizeConsole(tabs.HandleSelectEnvironment))
	mux.HandleFunc("/api/deployments/rollback", authorizeConsole(tabs.HandleRollback))
	mux.HandleFunc("/api/tenant/switch", authorizeConsole(tabs.HandleTenantSwitch))
}
