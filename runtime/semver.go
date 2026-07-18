//go:build !wasm

package runtime

import (
	"strconv"
	"strings"
)

// semverParts holds the parsed integer fields.
type semverParts struct {
	major int
	minor int
	patch int
}

func parseSemver(v string) (semverParts, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")

	// strip pre-release or build metadata (like -alpha, +build.1)
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return semverParts{}, false
	}

	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])

	if err1 != nil || err2 != nil || err3 != nil {
		return semverParts{}, false
	}

	return semverParts{major: major, minor: minor, patch: patch}, true
}

// SemverParse parses semver string into map representation.
func SemverParse(version interface{}) interface{} {
	vStr := toString(version)
	parts, ok := parseSemver(vStr)
	if !ok {
		return [2]interface{}{nil, "invalid semver string format"}
	}
	return map[string]interface{}{
		"major": float64(parts.major),
		"minor": float64(parts.minor),
		"patch": float64(parts.patch),
	}
}

// SemverCompare compares two semver strings: -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2.
func SemverCompare(v1, v2 interface{}) interface{} {
	p1, ok1 := parseSemver(toString(v1))
	p2, ok2 := parseSemver(toString(v2))
	if !ok1 || !ok2 {
		return [2]interface{}{nil, "invalid semver comparison target"}
	}

	if p1.major != p2.major {
		if p1.major < p2.major {
			return -1.0
		}
		return 1.0
	}
	if p1.minor != p2.minor {
		if p1.minor < p2.minor {
			return -1.0
		}
		return 1.0
	}
	if p1.patch != p2.patch {
		if p1.patch < p2.patch {
			return -1.0
		}
		return 1.0
	}
	return 0.0
}

// SemverSatisfies checks if version string satisfies the range requirement.
func SemverSatisfies(rangeStr, version interface{}) interface{} {
	r := strings.TrimSpace(toString(rangeStr))
	v := strings.TrimSpace(toString(version))

	vp, ok := parseSemver(v)
	if !ok {
		return false
	}

	if r == "*" || r == "" {
		return true
	}

	// Helper comparisons
	compareParts := func(a, b semverParts) int {
		if a.major != b.major {
			if a.major < b.major {
				return -1
			}
			return 1
		}
		if a.minor != b.minor {
			if a.minor < b.minor {
				return -1
			}
			return 1
		}
		if a.patch != b.patch {
			if a.patch < b.patch {
				return -1
			}
			return 1
		}
		return 0
	}

	// 1. Caret Range: ^1.2.3 -> >=1.2.3 and <2.0.0
	if strings.HasPrefix(r, "^") {
		targetStr := strings.TrimPrefix(r, "^")
		tp, ok := parseSemver(targetStr)
		if !ok {
			return false
		}
		// Must be >= target
		if compareParts(vp, tp) < 0 {
			return false
		}
		// Must be < major+1
		if vp.major >= tp.major+1 {
			return false
		}
		return true
	}

	// 2. Tilde Range: ~1.2.3 -> >=1.2.3 and <1.3.0
	if strings.HasPrefix(r, "~") {
		targetStr := strings.TrimPrefix(r, "~")
		tp, ok := parseSemver(targetStr)
		if !ok {
			return false
		}
		// Must be >= target
		if compareParts(vp, tp) < 0 {
			return false
		}
		// Must be < major.minor+1
		if vp.major != tp.major || vp.minor >= tp.minor+1 {
			return false
		}
		return true
	}

	// 3. Comparisons: >=, <=, >, <, =
	if strings.HasPrefix(r, ">=") {
		tp, ok := parseSemver(strings.TrimPrefix(r, ">="))
		return ok && compareParts(vp, tp) >= 0
	}
	if strings.HasPrefix(r, "<=") {
		tp, ok := parseSemver(strings.TrimPrefix(r, "<="))
		return ok && compareParts(vp, tp) <= 0
	}
	if strings.HasPrefix(r, ">") {
		tp, ok := parseSemver(strings.TrimPrefix(r, ">"))
		return ok && compareParts(vp, tp) > 0
	}
	if strings.HasPrefix(r, "<") {
		tp, ok := parseSemver(strings.TrimPrefix(r, "<"))
		return ok && compareParts(vp, tp) < 0
	}
	if strings.HasPrefix(r, "=") {
		tp, ok := parseSemver(strings.TrimPrefix(r, "="))
		return ok && compareParts(vp, tp) == 0
	}

	// Default to exact match
	tp, ok := parseSemver(r)
	return ok && compareParts(vp, tp) == 0
}
