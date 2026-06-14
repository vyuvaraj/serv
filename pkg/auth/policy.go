package auth

import (
	"encoding/json"
	"strings"
)

type StringOrArray []string

func (sa *StringOrArray) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*sa = []string{s}
		return nil
	}
	var a []string
	if err := json.Unmarshal(data, &a); err == nil {
		*sa = a
		return nil
	}
	return json.Unmarshal(data, sa)
}

type Statement struct {
	Effect   string        `json:"Effect"`   // "Allow" or "Deny"
	Action   StringOrArray `json:"Action"`   // e.g. "s3:GetObject" or ["s3:GetObject", "s3:PutObject"]
	Resource StringOrArray `json:"Resource"` // e.g. "arn:aws:s3:::mybucket/*" or ["arn:aws:s3:::mybucket"]
}

type Policy struct {
	Version   string      `json:"Version,omitempty"`
	Statement []Statement `json:"Statement"`
}

// IsAllowed evaluates the S3 S3-compatible S3 policies against action and resource.
func (p *Policy) IsAllowed(action, resource string) bool {
	action = strings.ToLower(action)
	resource = strings.ToLower(resource)

	matchedAllow := false

	for _, stmt := range p.Statement {
		effect := strings.ToLower(stmt.Effect)
		if effect != "allow" && effect != "deny" {
			continue
		}

		// Action match
		actionMatch := false
		for _, act := range stmt.Action {
			if matchWildcard(strings.ToLower(act), action) {
				actionMatch = true
				break
			}
		}
		if !actionMatch {
			continue
		}

		// Resource match
		resourceMatch := false
		for _, res := range stmt.Resource {
			if matchWildcard(strings.ToLower(res), resource) {
				resourceMatch = true
				break
			}
		}
		if !resourceMatch {
			continue
		}

		// Deny overrides Allow
		if effect == "deny" {
			return false
		}
		matchedAllow = true
	}

	return matchedAllow
}

func matchWildcard(pattern, val string) bool {
	if pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == val
	}
	if !strings.HasPrefix(val, parts[0]) {
		return false
	}
	val = val[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(val, parts[i])
		if idx == -1 {
			return false
		}
		val = val[idx+len(parts[i]):]
	}
	return strings.HasSuffix(val, parts[len(parts)-1])
}
