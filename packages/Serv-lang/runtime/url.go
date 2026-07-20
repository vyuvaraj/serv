//go:build !wasm

package runtime

import (
	"net/url"
)

// URLParse parses a URL string into scheme, host, path, query.
func URLParse(urlStr interface{}) interface{} {
	sStr := toString(urlStr)
	u, err := url.Parse(sStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	qMap := make(map[string]interface{})
	for k, v := range u.Query() {
		if len(v) > 0 {
			qMap[k] = v[0]
		}
	}
	return map[string]interface{}{
		"scheme": u.Scheme,
		"host":   u.Host,
		"path":   u.Path,
		"query":  qMap,
	}
}

// URLEncode escapes query string components.
func URLEncode(str interface{}) interface{} {
	sStr := toString(str)
	return url.QueryEscape(sStr)
}

// URLDecode unescapes query string components.
func URLDecode(str interface{}) interface{} {
	sStr := toString(str)
	decoded, err := url.QueryUnescape(sStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return decoded
}
