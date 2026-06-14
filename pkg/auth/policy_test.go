package auth

import (
	"encoding/json"
	"testing"
)

func TestPolicyEvaluation(t *testing.T) {
	policyJSON := `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Action": ["s3:GetObject", "s3:ListBucket"],
				"Resource": ["arn:aws:s3:::mybucket", "arn:aws:s3:::mybucket/*"]
			},
			{
				"Effect": "Deny",
				"Action": "s3:DeleteObject",
				"Resource": "arn:aws:s3:::mybucket/protected/*"
			}
		]
	}`

	var p Policy
	if err := json.Unmarshal([]byte(policyJSON), &p); err != nil {
		t.Fatalf("Failed to unmarshal policy: %v", err)
	}

	// 1. Allowed action on bucket
	if !p.IsAllowed("s3:ListBucket", "arn:aws:s3:::mybucket") {
		t.Error("Expected s3:ListBucket to be allowed on bucket")
	}

	// 2. Allowed action on object
	if !p.IsAllowed("s3:GetObject", "arn:aws:s3:::mybucket/photo.jpg") {
		t.Error("Expected s3:GetObject to be allowed on object")
	}

	// 3. Disallowed action (default deny)
	if p.IsAllowed("s3:PutObject", "arn:aws:s3:::mybucket/photo.jpg") {
		t.Error("Expected s3:PutObject to be denied (default deny)")
	}

	// 4. Denied action under deny resource rule
	if p.IsAllowed("s3:DeleteObject", "arn:aws:s3:::mybucket/protected/file.txt") {
		t.Error("Expected s3:DeleteObject to be denied on protected file")
	}
}

func TestPolicyWildcards(t *testing.T) {
	policyJSON := `{
		"Statement": [
			{
				"Effect": "Allow",
				"Action": "s3:*",
				"Resource": "arn:aws:s3:::public-bucket/*"
			}
		]
	}`

	var p Policy
	if err := json.Unmarshal([]byte(policyJSON), &p); err != nil {
		t.Fatalf("Failed to unmarshal policy: %v", err)
	}

	if !p.IsAllowed("s3:PutObject", "arn:aws:s3:::public-bucket/folder/subfolder/file.bin") {
		t.Error("Expected wildcard action and resource path match to be allowed")
	}
}
