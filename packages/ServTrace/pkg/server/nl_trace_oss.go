//go:build !enterprise

package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"servtrace/pkg/store"
)

// handleNaturalLanguageSearch is the OSS implementation of AI.15.
// It translates simple keyword queries into trace filters and returns
// matching TraceSummary records. For full NL incident summarisation
// with narrative generation, upgrade to Enterprise Edition.
func (s *Server) handleNaturalLanguageSearch(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	query := req.URL.Query().Get("q")
	filters, err := s.ResolveNaturalLanguageQuery(query)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	traces := s.traceStore.ListTraces()
	filtered := make([]store.TraceSummary, 0)

	for _, t := range traces {
		if svc, ok := filters["service"]; ok && !strings.Contains(strings.ToLower(t.Service), strings.ToLower(svc)) {
			continue
		}
		if errorFilter, ok := filters["error"]; ok && errorFilter == "true" && t.ErrorCount == 0 {
			continue
		}
		if minDuration, ok := filters["min_duration_ms"]; ok {
			if limit, err := strconv.ParseFloat(minDuration, 64); err == nil && t.DurationMs < limit {
				continue
			}
		}
		filtered = append(filtered, t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filtered)
}
