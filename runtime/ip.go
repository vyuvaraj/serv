//go:build !wasm

package runtime

import (
	"net"
)

// IPParse parses an IP address string and returns version and octets.
func IPParse(val interface{}) interface{} {
	ipStr := toString(val)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return [2]interface{}{nil, "invalid IP address"}
	}

	var version float64 = 4
	if ip.To4() == nil {
		version = 6
	}

	var octets []interface{}
	var rawBytes []byte
	if v4 := ip.To4(); v4 != nil {
		rawBytes = v4
	} else {
		rawBytes = ip
	}

	for _, b := range rawBytes {
		octets = append(octets, float64(b))
	}

	return map[string]interface{}{
		"version": version,
		"octets":  octets,
	}
}

// IPIsPrivate checks if IP is private subnet.
func IPIsPrivate(val interface{}) interface{} {
	ipStr := toString(val)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsPrivate()
}

// IPInCIDR checks if IP is in CIDR block.
func IPInCIDR(ipVal, cidrVal interface{}) interface{} {
	ipStr := toString(ipVal)
	cidrStr := toString(cidrVal)

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	_, ipnet, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return false
	}

	return ipnet.Contains(ip)
}

// IPVersion returns version string ("ipv4" or "ipv6").
func IPVersion(val interface{}) interface{} {
	ipStr := toString(val)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}
