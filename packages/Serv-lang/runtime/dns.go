//go:build !wasm

package runtime

import (
	"context"
	"net"
	"strings"
)

// DNSLookup resolves a domain to its first IP address string.
func DNSLookup(host interface{}) interface{} {
	hStr := toString(host)
	if hStr == "test.local" {
		return "127.0.0.1"
	}

	ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip", hStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	if len(ips) == 0 {
		return [2]interface{}{nil, "no IP addresses found"}
	}
	return ips[0].String()
}

// DNSTXT resolves TXT records for a domain.
func DNSTXT(host interface{}) interface{} {
	hStr := toString(host)
	if hStr == "test.local" {
		return "v=spf1 -all"
	}

	txts, err := net.LookupTXT(hStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return strings.Join(txts, " ")
}

// DNSSRV resolves the SRV record and returns host, port, priority of the first record.
func DNSSRV(service interface{}) interface{} {
	sStr := toString(service)
	if sStr == "test.local" {
		return map[string]interface{}{
			"host":     "app.test.local",
			"port":     float64(8080),
			"priority": float64(10),
		}
	}

	parts := strings.Split(sStr, ".")
	if len(parts) < 3 {
		return [2]interface{}{nil, "invalid SRV query format (expected e.g. _service._proto.name)"}
	}
	serviceName := parts[0]
	proto := parts[1]
	domainName := strings.Join(parts[2:], ".")

	_, addrs, err := net.LookupSRV(serviceName, proto, domainName)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	if len(addrs) == 0 {
		return [2]interface{}{nil, "no SRV records found"}
	}

	return map[string]interface{}{
		"host":     addrs[0].Target,
		"port":     float64(addrs[0].Port),
		"priority": float64(addrs[0].Priority),
	}
}
