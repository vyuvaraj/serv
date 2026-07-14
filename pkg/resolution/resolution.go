package resolution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"servregistry/pkg/registry"
)

func ParseSemver(v string) (int, int, int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid semver: %s", v)
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("invalid semver integers: %s", v)
	}
	return major, minor, patch, nil
}

func compareSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func parseTriplet(v string) ([3]int, error) {
	maj, min, pat, err := ParseSemver(v)
	return [3]int{maj, min, pat}, err
}

func matchSingleConstraint(constraint, versionStr string) bool {
	constraint = strings.TrimSpace(constraint)
	// Pre-release tags (containing "-") are never matched by range operators
	if strings.Contains(versionStr, "-") {
		return constraint == versionStr
	}

	vt, err := parseTriplet(versionStr)
	if err != nil {
		return false
	}

	switch {
	case constraint == "" || constraint == "*" || constraint == "latest":
		return true
	case constraint == versionStr:
		return true
	case strings.HasPrefix(constraint, "^"):
		rt, err := parseTriplet(strings.TrimPrefix(constraint, "^"))
		if err != nil {
			return false
		}
		// npm ^ semantics: if major > 0, pin major. If major == 0 and minor > 0, pin major+minor.
		// If major == 0 and minor == 0, pin all three.
		if rt[0] > 0 {
			return vt[0] == rt[0] && compareSemver(vt, rt) >= 0
		}
		if rt[1] > 0 {
			return vt[0] == 0 && vt[1] == rt[1] && vt[2] >= rt[2]
		}
		return vt[0] == 0 && vt[1] == 0 && vt[2] == rt[2]
	case strings.HasPrefix(constraint, "~"):
		rt, err := parseTriplet(strings.TrimPrefix(constraint, "~"))
		if err != nil {
			return false
		}
		return vt[0] == rt[0] && vt[1] == rt[1] && vt[2] >= rt[2]
	case strings.HasPrefix(constraint, ">="):
		rt, err := parseTriplet(strings.TrimPrefix(constraint, ">="))
		if err != nil {
			return false
		}
		return compareSemver(vt, rt) >= 0
	case strings.HasPrefix(constraint, "<="):
		rt, err := parseTriplet(strings.TrimPrefix(constraint, "<="))
		if err != nil {
			return false
		}
		return compareSemver(vt, rt) <= 0
	case strings.HasPrefix(constraint, ">"):
		rt, err := parseTriplet(strings.TrimPrefix(constraint, ">"))
		if err != nil {
			return false
		}
		return compareSemver(vt, rt) > 0
	case strings.HasPrefix(constraint, "<"):
		rt, err := parseTriplet(strings.TrimPrefix(constraint, "<"))
		if err != nil {
			return false
		}
		return compareSemver(vt, rt) < 0
	default:
		// exact match
		return constraint == versionStr
	}
}

func MatchSemver(rangeStr, versionStr string) bool {
	rangeStr = strings.TrimSpace(rangeStr)
	if rangeStr == "" || rangeStr == "*" || rangeStr == "latest" {
		return true
	}

	// Compound AND range: space-separated constraints (e.g. ">=1.2.3 <2.0.0")
	// All constraints must match.
	parts := strings.Fields(rangeStr)
	for _, part := range parts {
		if !matchSingleConstraint(part, versionStr) {
			return false
		}
	}
	return true
}


func ResolveBestVersion(rangeStr string, versions map[string]registry.VersionDetails) string {
	var bestVersion string
	var bestMaj, bestMin, bestPat int
	found := false

	for v := range versions {
		if MatchSemver(rangeStr, v) {
			maj, min, pat, err := ParseSemver(v)
			if err != nil {
				continue
			}
			if !found {
				bestVersion = v
				bestMaj, bestMin, bestPat = maj, min, pat
				found = true
				continue
			}
			if maj > bestMaj {
				bestVersion = v
				bestMaj, bestMin, bestPat = maj, min, pat
			} else if maj == bestMaj {
				if min > bestMin {
					bestVersion = v
					bestMaj, bestMin, bestPat = maj, min, pat
				} else if min == bestMin {
					if pat > bestPat {
						bestVersion = v
						bestMaj, bestMin, bestPat = maj, min, pat
					}
				}
			}
		}
	}

	return bestVersion
}

func ParseServTomlFromTarGz(data []byte) (string, string, []string, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", "", nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", nil, err
		}

		if filepath.Base(hdr.Name) == "serv.toml" {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, tr); err != nil {
				return "", "", nil, err
			}
			return ParseServToml(buf.String())
		}
	}
	return "", "", nil, fmt.Errorf("serv.toml not found in package archive")
}

func ParseServToml(content string) (string, string, []string, error) {
	var name, version string
	var dependencies []string

	lines := strings.Split(content, "\n")
	inDependenciesSection := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			if section == "dependencies" {
				inDependenciesSection = true
			} else {
				inDependenciesSection = false
			}
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		if (strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"")) ||
			(strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'")) {
			v = v[1 : len(v)-1]
		}

		if inDependenciesSection {
			dependencies = append(dependencies, fmt.Sprintf("%s@%s", k, v))
		} else {
			switch k {
			case "name":
				name = v
			case "version":
				version = v
			}
		}
	}
	return name, version, dependencies, nil
}
