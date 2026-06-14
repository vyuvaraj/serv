package storage

import (
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
