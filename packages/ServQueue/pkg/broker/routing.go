//go:build enterprise

package broker

import "strings"

// filterSemanticRoute is the enterprise implementation.
func (e *BrokerEngine) filterSemanticRoute(topic string, payload string) bool {
	if strings.Contains(topic, "support") {
		isBilling := strings.Contains(strings.ToLower(payload), "billing") || strings.Contains(strings.ToLower(payload), "invoice")
		if isBilling && !strings.Contains(topic, "billing") {
			return true // Filter this out (drop it)
		}
	}
	return false
}
