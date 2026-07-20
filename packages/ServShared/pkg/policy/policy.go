package policy

import (
	"encoding/json"
	"strings"
)

type PolicyRule struct {
	ID           string   `json:"id"`
	Action       string   `json:"action"` // "allow", "deny"
	Methods      []string `json:"methods"`
	Path         string   `json:"path"`
	Roles        []string `json:"roles,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	RateLimitRPM int      `json:"rate_limit_rpm,omitempty"`
	RedactFields []string `json:"redact_fields,omitempty"`
}

type PolicySchema struct {
	Version int          `json:"version"`
	Rules   []PolicyRule `json:"rules"`
}

func ParsePolicySchema(data []byte) (*PolicySchema, error) {
	var schema PolicySchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}

func EvaluatePolicy(
	reqMethod string,
	reqPath string,
	reqHeaders map[string]string,
	userRoles []string,
	schema *PolicySchema,
) (action string, redactFields []string, customRateLimit int, matched bool) {
	if schema == nil {
		return "allow", nil, 0, false
	}

	for _, rule := range schema.Rules {
		// 1. Match Method
		methodMatch := false
		for _, m := range rule.Methods {
			if m == "*" || strings.EqualFold(m, reqMethod) {
				methodMatch = true
				break
			}
		}
		if !methodMatch {
			continue
		}

		// 2. Match Path (Wildcard matching)
		if !matchPath(rule.Path, reqPath) {
			continue
		}

		// 3. Match Roles
		if len(rule.Roles) > 0 {
			roleMatch := false
			for _, reqRole := range rule.Roles {
				if reqRole == "*" {
					roleMatch = true
					break
				}
				for _, r := range userRoles {
					if strings.EqualFold(r, reqRole) {
						roleMatch = true
						break
					}
				}
			}
			if !roleMatch {
				continue
			}
		}

		// 4. Match Headers
		headerMatch := true
		for hName, hVal := range rule.Headers {
			actualVal := reqHeaders[strings.ToLower(hName)]
			if actualVal == "" {
				actualVal = reqHeaders[hName]
			}
			if actualVal != hVal {
				headerMatch = false
				break
			}
		}
		if !headerMatch {
			continue
		}

		// All matched!
		return rule.Action, rule.RedactFields, rule.RateLimitRPM, true
	}

	return "allow", nil, 0, false
}

func matchPath(pattern, path string) bool {
	if pattern == "*" || pattern == "/*" {
		return true
	}
	if pattern == path {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(path, prefix)
	}
	return false
}
