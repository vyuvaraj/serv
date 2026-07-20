package ai

import (
	"strings"
)

type AIMetricsResponse struct {
	TotalCostsUSD     float64        `json:"totalCostsUsd"`
	TotalToolCalls    int            `json:"totalToolCalls"`
	ActiveAgentsCount int            `json:"activeAgentsCount"`
	ToolCalls         []AIToolCall   `json:"toolCalls"`
	SafetyAlerts      []AISafetyAlert `json:"safetyAlerts"`
}

type AIToolCall struct {
	Timestamp  string  `json:"timestamp"`
	AgentName  string  `json:"agentName"`
	ToolCalled string  `json:"toolCalled"`
	Status     string  `json:"status"`
	TokensUsed int     `json:"tokensUsed"`
	CostUSD    float64 `json:"costUsd"`
}

type AISafetyAlert struct {
	Timestamp string `json:"timestamp"`
	AgentName string `json:"agentName"`
	Severity  string `json:"severity"`
	RuleName  string `json:"ruleName"`
	Message   string `json:"message"`
}

// GenerateIncidentResolutionSuggestion generates suggestions for resolving an incident using AI.
func GenerateIncidentResolutionSuggestion(incidentType, message string) string {
	var suggestions []string
	if strings.Contains(strings.ToLower(incidentType), "latency") || strings.Contains(strings.ToLower(message), "latency") {
		suggestions = append(suggestions, "Scale up the service replica count.", "Verify if database connection pooling limits are exceeded.")
	}
	if strings.Contains(strings.ToLower(incidentType), "error") || strings.Contains(strings.ToLower(message), "error") {
		suggestions = append(suggestions, "Check audit logs for recent deployment changes.", "Verify credentials rotation status.")
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "Monitor standard health probes.", "Collect trace diagnostic data.")
	}
	return "AI Recommended Remediation Steps:\n- " + strings.Join(suggestions, "\n- ")
}
