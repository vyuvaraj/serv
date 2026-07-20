package storage

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

var wordRegex = regexp.MustCompile(`[a-zA-Z0-9]+`)

// Tokenize text into words (lowercased)
func Tokenize(text string) []string {
	words := wordRegex.FindAllString(strings.ToLower(text), -1)
	return words
}

// Compute Term Frequency (TF) for a document
func ComputeTF(tokens []string) map[string]float64 {
	tf := make(map[string]float64)
	if len(tokens) == 0 {
		return tf
	}
	for _, word := range tokens {
		tf[word]++
	}
	for word, count := range tf {
		tf[word] = count / float64(len(tokens))
	}
	return tf
}

// Compute Cosine Similarity between two term-frequency vectors
func CosineSimilarity(vec1, vec2 map[string]float64) float64 {
	dotProduct := 0.0
	magnitude1 := 0.0
	magnitude2 := 0.0

	// We calculate dot product based on common terms
	for word, val1 := range vec1 {
		magnitude1 += val1 * val1
		if val2, exists := vec2[word]; exists {
			dotProduct += val1 * val2
		}
	}

	for _, val2 := range vec2 {
		magnitude2 += val2 * val2
	}

	if magnitude1 == 0.0 || magnitude2 == 0.0 {
		return 0.0
	}

	return dotProduct / (math.Sqrt(magnitude1) * math.Sqrt(magnitude2))
}

func fnv1a(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// Global hooks for ONNX implementation. If CGO is enabled,
// vector_onnx.go will register these hooks in its init() block.
var (
	onnxInitFunc func(string, string, string) error
	onnxEvalFunc func(string) ([]float64, error)
)

// GenerateEmbedding maps text into a normalized dense vector.
// If ONNX is loaded successfully, it returns a 384-dimensional semantic embedding.
// Otherwise, it falls back to a 128-dimensional unigram/bigram hashing trick embedding.
func GenerateEmbedding(text string) []float64 {
	if onnxEvalFunc != nil {
		vec, err := onnxEvalFunc(text)
		if err == nil {
			return vec
		}
	}
	return GenerateFallbackEmbedding(text)
}

// GenerateFallbackEmbedding maps text into a normalized 128-dimensional dense vector
func GenerateFallbackEmbedding(text string) []float64 {
	tokens := Tokenize(text)
	D := 128
	vec := make([]float64, D)
	if len(tokens) == 0 {
		return vec
	}

	// Unigrams
	for _, t := range tokens {
		h1 := fnv1a(t)
		idx := int(h1 % uint32(D))
		sign := 1.0
		if (h1 & 1) == 0 {
			sign = -1.0
		}
		vec[idx] += sign
	}

	// Bigrams
	for i := 0; i < len(tokens)-1; i++ {
		bigram := tokens[i] + " " + tokens[i+1]
		h1 := fnv1a(bigram)
		idx := int(h1 % uint32(D))
		sign := 1.0
		if (h1 & 1) == 0 {
			sign = -1.0
		}
		vec[idx] += sign * 0.5
	}

	// L2 Normalize
	sumSq := 0.0
	for _, val := range vec {
		sumSq += val * val
	}
	if sumSq > 0 {
		mag := math.Sqrt(sumSq)
		for i := range vec {
			vec[i] /= mag
		}
	}

	return vec
}

// CosineSimilarityDense computes similarity between two dense vectors
func CosineSimilarityDense(v1, v2 []float64) float64 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0.0
	}
	dot := 0.0
	mag1 := 0.0
	mag2 := 0.0
	for i := range v1 {
		dot += v1[i] * v2[i]
		mag1 += v1[i] * v1[i]
		mag2 += v2[i] * v2[i]
	}
	if mag1 == 0.0 || mag2 == 0.0 {
		return 0.0
	}
	return dot / (math.Sqrt(mag1) * math.Sqrt(mag2))
}

// CosineDistance computes distance between two dense vectors (1.0 - CosineSimilarity)
func CosineDistance(v1, v2 []float64) float64 {
	return 1.0 - CosineSimilarityDense(v1, v2)
}

// InitializeONNX initializes the ONNX runtime shared library and model.
// If CGO is disabled or registration hooks are missing, it returns a descriptive error.
func InitializeONNX(sharedLibPath, modelPath, vocabPath string) error {
	if onnxInitFunc == nil {
		return fmt.Errorf("ONNX embeddings not supported in this build (requires CGO)")
	}
	return onnxInitFunc(sharedLibPath, modelPath, vocabPath)
}
