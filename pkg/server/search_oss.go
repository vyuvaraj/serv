//go:build !enterprise

package server

import (
	"errors"
)

func (s *Server) ResolveNaturalLanguageQuery(query string) (map[string]string, error) {
	return nil, errors.New("Enterprise Edition required for natural language log/trace search")
}
