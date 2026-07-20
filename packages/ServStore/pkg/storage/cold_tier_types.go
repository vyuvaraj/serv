package storage

import "time"

// ColdTierConfig defines the options for the background cold-storage sweep.
type ColdTierConfig struct {
	MinAge       time.Duration `json:"min_age"`
	SweepInterval time.Duration `json:"sweep_interval"`
	TargetBucket  string        `json:"target_bucket"`
}

// ColdTierManager implements the background scanner and archival logic.
type ColdTierManager struct {
	store *LocalStore
	cfg   ColdTierConfig
}

// stubPath returns the .cold stub path (single arg: data file path).
func stubPath(dataPath string) string {
	return dataPath + ".cold"
}
