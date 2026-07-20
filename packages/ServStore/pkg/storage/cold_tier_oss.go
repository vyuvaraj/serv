//go:build !enterprise

package storage

import "fmt"

// IsIntelligentTieringSupported indicates if Glacier and lifecycle auto-tiering is supported.
const IsIntelligentTieringSupported = false

// NewColdTierManager returns nil in OSS (feature disabled).
func NewColdTierManager(_ *ColdTierConfig, _ *LocalStore) *ColdTierManager {
	return nil
}

// Start is a no-op in OSS.
func (c *ColdTierManager) Start() {}

// Stop is a no-op in OSS.
func (c *ColdTierManager) Stop() {}

// FetchBack is a no-op in OSS (returns error indicating EE required).
func (c *ColdTierManager) FetchBack(_ interface{}, _ string) error {
	return fmt.Errorf("cold tier re-hydration requires Enterprise Edition")
}



// GetColdTierConfig returns empty config and false in OSS (cold tier not available).
func (s *LocalStore) GetColdTierConfig() (ColdTierConfig, bool) {
	return ColdTierConfig{}, false
}

// SetColdTier is a no-op in OSS (cold storage tiering requires Enterprise Edition).
func (s *LocalStore) SetColdTier(_ ColdTierConfig) error {
	return nil
}
