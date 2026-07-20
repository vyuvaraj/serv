package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func ConfigureProxyDirector(
	proxy *httputil.ReverseProxy,
	target *url.URL,
	prefix string,
	defaultToken string,
	getProxyActionName func(string, string) string,
	addAuditLog func(string, string, string, string, int),
) {
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		if defaultToken != "" && req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", "Bearer "+defaultToken)
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		req := resp.Request
		if req != nil && (req.Method == "POST" || req.Method == "PUT" || req.Method == "DELETE") {
			user := req.Header.Get("X-Console-User")
			action := getProxyActionName(prefix, req.URL.Path)
			addAuditLog(user, action, req.Method, req.URL.Path, resp.StatusCode)
		}
		return nil
	}
}
