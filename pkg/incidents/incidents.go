package incidents

import (
	"encoding/json"
	"net/http"
	"sync"
)

type SLO struct {
	ServiceName  string  `json:"service_name"`
	TargetP99Ms  float64 `json:"target_p99_ms"`
	ActualP99Ms  float64 `json:"actual_p99_ms"`
	TargetError  float64 `json:"target_error_rate_pct"`
	ActualError  float64 `json:"actual_error_rate_pct"`
	BudgetRemain float64 `json:"budget_remaining_pct"`
}

type SLOTracker struct {
	mu   sync.RWMutex
	slos map[string]SLO
}

func NewSLOTracker() *SLOTracker {
	t := &SLOTracker{
		slos: make(map[string]SLO),
	}
	t.slos["ServGate"] = SLO{ServiceName: "ServGate", TargetP99Ms: 200, ActualP99Ms: 45, TargetError: 1.0, ActualError: 0.05, BudgetRemain: 98.4}
	t.slos["ServStore"] = SLO{ServiceName: "ServStore", TargetP99Ms: 150, ActualP99Ms: 120, TargetError: 0.5, ActualError: 0.1, BudgetRemain: 92.5}
	return t
}

func (t *SLOTracker) HandleSLO(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t.slos)
}
