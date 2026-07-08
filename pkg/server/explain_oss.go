//go:build !enterprise

package server

import (
	"errors"
)

func (s *Server) ExplainAnomaly(traceID string) (map[string]interface{}, error) {
	return nil, errors.New("Enterprise Edition required for anomaly explanation")
}
