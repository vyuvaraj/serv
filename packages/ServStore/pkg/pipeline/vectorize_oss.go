//go:build !enterprise

package pipeline

import "errors"

// TriggerVectorization is an OSS stub for the AI.13 Automatic Vectorization Pipeline.
// In Enterprise Edition, this function automatically chunks the uploaded object content
// and generates TF-IDF vector embeddings stored alongside the object.
func TriggerVectorization(bucket, key string, content []byte) error {
	return errors.New("Enterprise Edition required for automatic vectorization pipeline")
}
