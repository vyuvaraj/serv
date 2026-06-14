package s3

import "encoding/xml"

const xmlNamespace = "http://s3.amazonaws.com/doc/2006-03-01/"

type ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

type ListAllMyBucketsResult struct {
	XMLName xml.Name       `xml:"ListAllMyBucketsResult"`
	Xmlns   string         `xml:"xmlns,attr"`
	Buckets []BucketResult `xml:"Buckets>Bucket"`
	Owner   OwnerResult    `xml:"Owner"`
}

type BucketResult struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type OwnerResult struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type ListBucketResult struct {
	XMLName        xml.Name       `xml:"ListBucketResult"`
	Xmlns          string         `xml:"xmlns,attr"`
	Name           string         `xml:"Name"`
	Prefix         string         `xml:"Prefix"`
	Marker         string         `xml:"Marker"`
	MaxKeys        int            `xml:"MaxKeys"`
	Delimiter      string         `xml:"Delimiter,omitempty"`
	IsTruncated    bool           `xml:"IsTruncated"`
	Contents       []ObjectResult `xml:"Contents"`
	CommonPrefixes []PrefixResult `xml:"CommonPrefixes,omitempty"`
}

type ObjectResult struct {
	Key          string      `xml:"Key"`
	LastModified string      `xml:"LastModified"`
	ETag         string      `xml:"ETag"`
	Size         int64       `xml:"Size"`
	StorageClass string      `xml:"StorageClass"`
	Owner        OwnerResult `xml:"Owner"`
}

type PrefixResult struct {
	Prefix string `xml:"Prefix"`
}

type ListVersionsResult struct {
	XMLName         xml.Name             `xml:"ListVersionsResult"`
	Xmlns           string               `xml:"xmlns,attr"`
	Name            string               `xml:"Name"`
	Prefix          string               `xml:"Prefix"`
	KeyMarker       string               `xml:"KeyMarker"`
	VersionIdMarker string               `xml:"VersionIdMarker"`
	MaxKeys         int                  `xml:"MaxKeys"`
	Delimiter       string               `xml:"Delimiter,omitempty"`
	IsTruncated     bool                 `xml:"IsTruncated"`
	Version         []VersionResult      `xml:"Version"`
	DeleteMarker    []DeleteMarkerResult `xml:"DeleteMarker"`
	CommonPrefixes  []PrefixResult       `xml:"CommonPrefixes,omitempty"`
}

type VersionResult struct {
	Key          string      `xml:"Key"`
	VersionId    string      `xml:"VersionId"`
	IsLatest     bool        `xml:"IsLatest"`
	LastModified string      `xml:"LastModified"`
	ETag         string      `xml:"ETag"`
	Size         int64       `xml:"Size"`
	StorageClass string      `xml:"StorageClass"`
	Owner        OwnerResult `xml:"Owner"`
}

type DeleteMarkerResult struct {
	Key          string      `xml:"Key"`
	VersionId    string      `xml:"VersionId"`
	IsLatest     bool        `xml:"IsLatest"`
	LastModified string      `xml:"LastModified"`
	Owner        OwnerResult `xml:"Owner"`
}

type VersioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Xmlns   string   `xml:"xmlns,attr"`
	Status  string   `xml:"Status,omitempty"` // "Enabled" or "Suspended"
}
