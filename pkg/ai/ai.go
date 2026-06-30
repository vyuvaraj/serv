package ai

import (
	"strings"
)

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
