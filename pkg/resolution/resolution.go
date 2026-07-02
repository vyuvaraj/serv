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

func MatchSemver(rangeStr, versionStr string) bool {
	if rangeStr == "" || rangeStr == "*" || rangeStr == "latest" {
		return true
	}
	if rangeStr == versionStr {
		return true
	}

	vMaj, vMin, vPat, err := ParseSemver(versionStr)
	if err != nil {
		return false
	}

	if strings.HasPrefix(rangeStr, "^") {
		rStr := strings.TrimPrefix(rangeStr, "^")
		rMaj, rMin, rPat, err := ParseSemver(rStr)
		if err != nil {
			return false
		}
		if vMaj != rMaj {
			return false
		}
		if vMin < rMin {
			return false
		}
		if vMin == rMin && vPat < rPat {
			return false
		}
		return true
	}

	if strings.HasPrefix(rangeStr, "~") {
		rStr := strings.TrimPrefix(rangeStr, "~")
		rMaj, rMin, rPat, err := ParseSemver(rStr)
		if err != nil {
			return false
		}
		if vMaj != rMaj || vMin != rMin {
			return false
		}
		if vPat < rPat {
			return false
		}
		return true
	}

	return false
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
			if k == "name" {
				name = v
			} else if k == "version" {
				version = v
			}
		}
	}
	return name, version, dependencies, nil
}
