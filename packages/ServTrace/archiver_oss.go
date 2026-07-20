//go:build !enterprise

package main

import (
	"log"
	"github.com/vyuvaraj/serv/packages/ServTrace/pkg/store"
)

func SetupColdTierArchiver(ts *store.Store) {
	ts.OnEvict = func(traceID string, spans []store.Span) {
		log.Printf("Cold Tier: Evicting trace %s (Archival skipped in Open-Source Edition)", traceID)
	}
}
