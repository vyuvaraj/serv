//go:build !wasm

package runtime

import (
	"regexp"
)

func toString(val interface{}) string {
	if s, ok := val.(string); ok {
		return s
	}
	return ""
}

// RegexMatch returns true if the pattern matches the value.
func RegexMatch(pattern interface{}, value interface{}) interface{} {
	pStr := toString(pattern)
	vStr := toString(value)
	matched, err := regexp.MatchString(pStr, vStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return matched
}

// RegexFind returns the first match of the pattern in the value.
func RegexFind(pattern interface{}, value interface{}) interface{} {
	pStr := toString(pattern)
	vStr := toString(value)
	re, err := regexp.Compile(pStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return re.FindString(vStr)
}

// RegexReplace replaces all matches of the pattern in the value with the replacement.
func RegexReplace(pattern interface{}, value interface{}, replacement interface{}) interface{} {
	pStr := toString(pattern)
	vStr := toString(value)
	rStr := toString(replacement)
	re, err := regexp.Compile(pStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return re.ReplaceAllString(vStr, rStr)
}
