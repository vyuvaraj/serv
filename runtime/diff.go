//go:build !wasm

package runtime

import (
	"fmt"
	"strings"
)

// DiffText computes unified diff style comparison line-by-line.
func DiffText(aIn, bIn interface{}) interface{} {
	aStr := toString(aIn)
	bStr := toString(bIn)

	a := strings.Split(strings.ReplaceAll(aStr, "\r\n", "\n"), "\n")
	b := strings.Split(strings.ReplaceAll(bStr, "\r\n", "\n"), "\n")

	m, n := len(a), len(b)
	
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = dp[i-1][j]
				if dp[i][j-1] > dp[i][j] {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	var diffLines []string
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && a[i-1] == b[j-1] {
			diffLines = append(diffLines, "  "+a[i-1])
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			diffLines = append(diffLines, "+ "+b[j-1])
			j--
		} else if i > 0 && (j == 0 || dp[i][j-1] < dp[i-1][j]) {
			diffLines = append(diffLines, "- "+a[i-1])
			i--
		}
	}

	for k := 0; k < len(diffLines)/2; k++ {
		diffLines[k], diffLines[len(diffLines)-1-k] = diffLines[len(diffLines)-1-k], diffLines[k]
	}

	return strings.Join(diffLines, "\n")
}

// DiffJSON recursively compares two JSON-like objects and returns a list of change maps.
func DiffJSON(aIn, bIn interface{}) interface{} {
	var patches []interface{}
	diffJSONRecursive(aIn, bIn, "", &patches)
	return patches
}

func diffJSONRecursive(a, b interface{}, path string, patches *[]interface{}) {
	if a == nil && b == nil {
		return
	}
	if a == nil {
		*patches = append(*patches, map[string]interface{}{
			"op":    "add",
			"path":  path,
			"value": b,
		})
		return
	}
	if b == nil {
		*patches = append(*patches, map[string]interface{}{
			"op":       "remove",
			"path":     path,
			"oldValue": a,
		})
		return
	}

	mapA, okA := a.(map[string]interface{})
	mapB, okB := b.(map[string]interface{})
	if okA && okB {
		for k, valA := range mapA {
			subPath := path + "/" + k
			if valB, exists := mapB[k]; exists {
				diffJSONRecursive(valA, valB, subPath, patches)
			} else {
				*patches = append(*patches, map[string]interface{}{
					"op":       "remove",
					"path":     subPath,
					"oldValue": valA,
				})
			}
		}
		for k, valB := range mapB {
			subPath := path + "/" + k
			if _, exists := mapA[k]; !exists {
				*patches = append(*patches, map[string]interface{}{
					"op":    "add",
					"path":  subPath,
					"value": valB,
				})
			}
		}
		return
	}

	sliceA, okA2 := a.([]interface{})
	sliceB, okB2 := b.([]interface{})
	if okA2 && okB2 {
		minLen := len(sliceA)
		if len(sliceB) < minLen {
			minLen = len(sliceB)
		}
		for i := 0; i < minLen; i++ {
			diffJSONRecursive(sliceA[i], sliceB[i], fmt.Sprintf("%s/%d", path, i), patches)
		}
		if len(sliceA) > minLen {
			for i := minLen; i < len(sliceA); i++ {
				*patches = append(*patches, map[string]interface{}{
					"op":       "remove",
					"path":     fmt.Sprintf("%s/%d", path, i),
					"oldValue": sliceA[i],
				})
			}
		}
		if len(sliceB) > minLen {
			for i := minLen; i < len(sliceB); i++ {
				*patches = append(*patches, map[string]interface{}{
					"op":    "add",
					"path":  fmt.Sprintf("%s/%d", path, i),
					"value": sliceB[i],
				})
			}
		}
		return
	}

	if !Equal(a, b) {
		*patches = append(*patches, map[string]interface{}{
			"op":       "replace",
			"path":     path,
			"oldValue": a,
			"value":    b,
		})
	}
}
