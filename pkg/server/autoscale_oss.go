//go:build !enterprise

package server

// IsAutoscaleSupported flags if the auto-scaling loop is supported in this build.
const IsAutoscaleSupported = false

func (s *Server) StartAutoscaleLoop() {
	// No-op in open-source
}

func (s *Server) StopAutoscaleLoopForTest() {
	// No-op in open-source
}
