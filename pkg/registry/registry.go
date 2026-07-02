package registry

import (
	"time"
)

type PackageIndexItem struct {
	Name         string    `json:"name"`
	Latest       string    `json:"latest"`
	Versions     []string  `json:"versions"`
	LastModified time.Time `json:"lastModified"`
}

type PackageMetadata struct {
	Name           string                    `json:"name"`
	Versions       map[string]VersionDetails `json:"versions"`
	Deprecated     bool                      `json:"deprecated,omitempty"`
	DeprecationMsg string                    `json:"deprecationMsg,omitempty"`
}

type VersionDetails struct {
	Version        string   `json:"version"`
	Dependencies   []string `json:"dependencies"`
	Size           int64    `json:"size"`
	PublishedAt    string   `json:"publishedAt"`
	Deprecated     bool     `json:"deprecated,omitempty"`
	DeprecationMsg string   `json:"deprecationMsg,omitempty"`
}

type PackageInfo struct {
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
}
