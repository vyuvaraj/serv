//go:build !enterprise

package handlers

import "net/http"

// IsVisualDesignerSupported indicates if the real-time visual DAG designer is supported.
const IsVisualDesignerSupported = false

// HandleDesignerSave is the open-source fallback handler for the visual designer saving endpoint.
func (ctx *HandlerContext) HandleDesignerSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(`{"error":"Real-Time Visual DAG Designer requires ServFlow Enterprise Edition"}`))
}
