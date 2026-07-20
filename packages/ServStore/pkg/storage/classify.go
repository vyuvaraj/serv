package storage

import (
	"fmt"
	"strings"
)

// AutoClassify performs heuristic classification on ingested files
// and returns a map of tags to be stored with the object version.
func AutoClassify(key string, contentType string, content []byte) map[string]string {
	tags := make(map[string]string)

	contentStr := strings.ToLower(string(content))
	keyLower := strings.ToLower(key)

	class := "unknown"
	confidence := 0.5

	if strings.Contains(contentType, "image") || strings.HasSuffix(keyLower, ".png") || strings.HasSuffix(keyLower, ".jpg") || strings.HasSuffix(keyLower, ".jpeg") || strings.HasSuffix(keyLower, ".gif") {
		class = "image"
		confidence = 0.95
	} else if strings.Contains(contentType, "audio") || strings.HasSuffix(keyLower, ".mp3") || strings.HasSuffix(keyLower, ".wav") || strings.HasSuffix(keyLower, ".ogg") {
		class = "audio"
		confidence = 0.95
	} else if strings.HasSuffix(keyLower, ".log") || strings.Contains(contentStr, "[info]") || strings.Contains(contentStr, "[error]") || strings.Contains(contentStr, "[warn]") || strings.Contains(contentStr, "level=") {
		class = "log"
		confidence = 0.85
	} else if strings.Contains(contentStr, "invoice") || strings.Contains(contentStr, "billing") || strings.Contains(contentStr, "receipt") || strings.Contains(keyLower, "invoice") || strings.Contains(keyLower, "receipt") {
		class = "invoice"
		confidence = 0.90
	} else if strings.Contains(contentStr, "contract") || strings.Contains(contentStr, "agreement") || strings.Contains(contentStr, "treaty") || strings.Contains(keyLower, "contract") || strings.Contains(keyLower, "agreement") {
		class = "contract"
		confidence = 0.90
	} else if strings.Contains(contentType, "pdf") || strings.HasSuffix(keyLower, ".pdf") {
		class = "document"
		confidence = 0.80
	} else if strings.HasSuffix(keyLower, ".txt") || strings.HasSuffix(keyLower, ".md") || strings.Contains(contentType, "text/") {
		class = "document"
		confidence = 0.70
	}

	tags["ai-class"] = class
	tags["ai-confidence"] = fmt.Sprintf("%.2f", confidence)
	return tags
}
