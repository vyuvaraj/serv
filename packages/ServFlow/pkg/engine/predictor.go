//go:build enterprise

package engine

// IsAiScalingPredictorSupported indicates if the telemetry-driven AI scaling predictor is supported.
const IsAiScalingPredictorSupported = true

// Note: The actual queue telemetry forecasting loop and preemptive runner cluster
// spawning implementation resides in the private servverse-ee overlay repository.
