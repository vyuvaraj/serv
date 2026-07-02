package main

import (
	"regexp"
	"strings"
	"time"
)

type Migration struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	AppliedAt time.Time `json:"applied_at"`
	SQL       string    `json:"sql,omitempty"`
	Rollback  string    `json:"rollback,omitempty"`
}

var (
	createTableRegex = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(\w+)`)
	dropTableRegex   = regexp.MustCompile(`(?i)DROP\s+TABLE\s+(\w+)`)
)

func ParseTablesFromSQL(sql string) (created []string, dropped []string) {
	queries := strings.Split(sql, ";")
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if matches := createTableRegex.FindStringSubmatch(q); len(matches) > 1 {
			created = append(created, matches[1])
		}
		if matches := dropTableRegex.FindStringSubmatch(q); len(matches) > 1 {
			dropped = append(dropped, matches[1])
		}
	}
	return
}
