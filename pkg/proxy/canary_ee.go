//go:build enterprise

package proxy

import (
	"log"
	"time"
)

func (h *GatewayHandler) startCanaryPromotionLoop() {
	log.Println("[EE CANARY ENGINE] Starting Enterprise Edition Canary Promotion & SLO Guardrails Loop")
	ticker := time.NewTicker(200 * time.Millisecond) // Fast ticker for responsive testing
	lastPromoted := make(map[string]time.Time)

	for range ticker.C {
		h.routesMu.Lock()
		for i, route := range h.routes {
			if !route.CanaryAutoPromote || len(route.TargetsWeighted) < 2 {
				continue
			}

			stable := &h.routes[i].TargetsWeighted[0]
			canary := &h.routes[i].TargetsWeighted[1]

			if canary.Weight >= 100 {
				h.routes[i].CanaryAutoPromote = false
				log.Printf("[EE CANARY ENGINE] Canary target %s promoted to 100%% successfully. Handing over to stable configuration.", canary.URL)
				continue
			}

			h.canaryStatsMu.Lock()
			stats, exists := h.canaryStats[canary.URL]
			var total, errors int64
			if exists {
				total = stats.TotalCalls
				errors = stats.ErrorCalls
			}
			h.canaryStatsMu.Unlock()

			maxErrRate := route.CanaryMaxErrorRate
			if maxErrRate <= 0 {
				maxErrRate = 0.01
			}
			if total >= 3 && float64(errors)/float64(total) > maxErrRate {
				log.Printf("[EE CANARY ENGINE] [SLO VIOLATION ALERT] Error rate on %s is %.2f%% (exceeds budget %.2f%%). Initiating automatic zero-downtime rollback!", canary.URL, float64(errors)/float64(total)*100, maxErrRate*100)
				stable.Weight = 100
				canary.Weight = 0
				h.routes[i].CanaryAutoPromote = false

				h.canaryStatsMu.Lock()
				delete(h.canaryStats, canary.URL)
				h.canaryStatsMu.Unlock()
				continue
			}

			interval := time.Duration(route.CanaryPromoteSec) * time.Second
			if interval <= 0 {
				interval = 1 * time.Second // Fast promote defaults for testing
			}
			lastTime, ok := lastPromoted[route.Prefix]
			if !ok {
				lastPromoted[route.Prefix] = time.Now()
				continue
			}

			if time.Since(lastTime) >= interval {
				step := route.CanaryPromoteStep
				if step <= 0 {
					step = 10
				}
				canary.Weight += step
				if canary.Weight > 100 {
					canary.Weight = 100
				}
				stable.Weight = 100 - canary.Weight
				log.Printf("[EE CANARY ENGINE] [STEP PROMOTE] Promoting canary %s weight to %d%% (stable %d%%)", canary.URL, canary.Weight, stable.Weight)
				lastPromoted[route.Prefix] = time.Now()

				h.canaryStatsMu.Lock()
				if stats != nil {
					stats.TotalCalls = 0
					stats.ErrorCalls = 0
				}
				h.canaryStatsMu.Unlock()
			}
		}
		h.routesMu.Unlock()
	}
}
