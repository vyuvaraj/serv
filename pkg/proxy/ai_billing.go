//go:build enterprise

package proxy

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// TokenUsage represents extracted token counts from an LLM response.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// UsageRecord represents a single LLM usage event.
type UsageRecord struct {
	Timestamp  time.Time  `json:"timestamp"`
	Route      string     `json:"route"`
	Model      string     `json:"model"`
	TenantID   string     `json:"tenant_id"`
	AgentID    string     `json:"agent_id,omitempty"`
	Usage      TokenUsage `json:"usage"`
	CostUSD    float64    `json:"cost_usd"`
	IsFallback bool       `json:"is_fallback"`
}

// BudgetConfig defines spending limits per tenant or route.
type BudgetConfig struct {
	MaxTokensPerMinute int     `json:"max_tokens_per_minute"`
	MaxCostPerDay      float64 `json:"max_cost_per_day_usd"`
	AlertThreshold     float64 `json:"alert_threshold_percent"` // Alert at X% of budget
}

// AIBillingTracker tracks token usage and enforces budgets.
type AIBillingTracker struct {
	mu       sync.RWMutex
	records  []UsageRecord
	budgets  map[string]*BudgetConfig // key: tenant_id or route prefix
	modelCosts map[string]float64     // cost per 1K tokens by model name
	maxRecords int
}

// NewAIBillingTracker creates a new billing tracker with default model costs.
func NewAIBillingTracker() *AIBillingTracker {
	return &AIBillingTracker{
		records:    make([]UsageRecord, 0, 10000),
		budgets:    make(map[string]*BudgetConfig),
		maxRecords: 50000,
		modelCosts: map[string]float64{
			// Cost per 1K tokens (input+output average)
			"gpt-4":           0.03,
			"gpt-4-turbo":     0.01,
			"gpt-4o":          0.005,
			"gpt-4o-mini":     0.00015,
			"gpt-3.5-turbo":   0.0015,
			"claude-3-opus":   0.015,
			"claude-3-sonnet": 0.003,
			"claude-3-haiku":  0.00025,
			"claude-3.5-sonnet": 0.003,
			"ollama":          0.0, // Local, free
			"default":         0.002,
		},
	}
}

// ExtractUsageFromResponse parses token usage from an OpenAI-compatible response body.
func ExtractUsageFromResponse(body []byte) *TokenUsage {
	var resp struct {
		Usage *TokenUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	return resp.Usage
}

// TrackUsage records a token usage event and returns whether budget is exceeded.
func (t *AIBillingTracker) TrackUsage(route, model, tenantID, agentID string, usage *TokenUsage, isFallback bool) (budgetExceeded bool) {
	if usage == nil || usage.TotalTokens == 0 {
		return false
	}

	cost := t.calculateCost(model, usage.TotalTokens)

	record := UsageRecord{
		Timestamp:  time.Now(),
		Route:      route,
		Model:      model,
		TenantID:   tenantID,
		AgentID:    agentID,
		Usage:      *usage,
		CostUSD:    cost,
		IsFallback: isFallback,
	}

	t.mu.Lock()
	// Evict oldest if at capacity
	if len(t.records) >= t.maxRecords {
		t.records = t.records[len(t.records)/2:]
	}
	t.records = append(t.records, record)
	t.mu.Unlock()

	// Check budget
	if budget, ok := t.budgets[tenantID]; ok {
		dailyCost := t.GetDailyCost(tenantID)
		if budget.MaxCostPerDay > 0 && dailyCost > budget.MaxCostPerDay {
			log.Printf("[ai-billing] Budget exceeded for tenant %s: $%.4f / $%.4f daily limit", tenantID, dailyCost, budget.MaxCostPerDay)
			return true
		}
	}

	return false
}

// SetBudget configures a spending limit for a tenant or route.
func (t *AIBillingTracker) SetBudget(key string, config *BudgetConfig) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.budgets[key] = config
}

// GetDailyCost returns the total cost for a tenant today.
func (t *AIBillingTracker) GetDailyCost(tenantID string) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	today := time.Now().Truncate(24 * time.Hour)
	var total float64
	for _, r := range t.records {
		if r.TenantID == tenantID && r.Timestamp.After(today) {
			total += r.CostUSD
		}
	}
	return total
}

// GetMetrics returns aggregate billing metrics for the dashboard.
func (t *AIBillingTracker) GetMetrics() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	totalTokens := 0
	totalCost := 0.0
	modelBreakdown := make(map[string]int)
	routeBreakdown := make(map[string]float64)
	tenantBreakdown := make(map[string]float64)

	today := time.Now().Truncate(24 * time.Hour)
	todayTokens := 0
	todayCost := 0.0

	for _, r := range t.records {
		totalTokens += r.Usage.TotalTokens
		totalCost += r.CostUSD
		modelBreakdown[r.Model] += r.Usage.TotalTokens
		routeBreakdown[r.Route] += r.CostUSD
		tenantBreakdown[r.TenantID] += r.CostUSD

		if r.Timestamp.After(today) {
			todayTokens += r.Usage.TotalTokens
			todayCost += r.CostUSD
		}
	}

	return map[string]interface{}{
		"total_tokens":      totalTokens,
		"total_cost_usd":    totalCost,
		"today_tokens":      todayTokens,
		"today_cost_usd":    todayCost,
		"total_requests":    len(t.records),
		"model_breakdown":   modelBreakdown,
		"route_breakdown":   routeBreakdown,
		"tenant_breakdown":  tenantBreakdown,
	}
}

// GetRecentRecords returns the last N usage records.
func (t *AIBillingTracker) GetRecentRecords(limit int) []UsageRecord {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if limit <= 0 || limit > len(t.records) {
		limit = len(t.records)
	}
	start := len(t.records) - limit
	if start < 0 {
		start = 0
	}
	result := make([]UsageRecord, limit)
	copy(result, t.records[start:])
	return result
}

func (t *AIBillingTracker) calculateCost(model string, tokens int) float64 {
	costPer1K, ok := t.modelCosts[model]
	if !ok {
		// Try prefix matching (e.g., "gpt-4o-2024-05-13" matches "gpt-4o")
		for prefix, cost := range t.modelCosts {
			if len(model) > len(prefix) && model[:len(prefix)] == prefix {
				costPer1K = cost
				ok = true
				break
			}
		}
		if !ok {
			costPer1K = t.modelCosts["default"]
		}
	}
	return float64(tokens) / 1000.0 * costPer1K
}
