package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Metrics
var (
	metricsCounters = make(map[string]int64)
	metricsGauges   MapStringFloat
	metricsMu       sync.RWMutex
)

type MapStringFloat struct {
	m map[string]float64
	sync.RWMutex
}

func MetricInc(name string) {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	metricsCounters[name]++
}

func MetricGauge(name string, val float64) {
	metricsGauges.Lock()
	defer metricsGauges.Unlock()
	metricsGauges.m[name] = val
}

// HTTP Client
func HTTPGet(url string) interface{} {
	endSpan := TraceHTTPClient("GET", url)
	start := time.Now()
	MetricInc("http_client_requests_total")

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		MetricInc("http_client_errors_total")
		endSpan(0)
		return [2]interface{}{nil, fmt.Sprintf("HTTP GET request failed for %s: %s", url, err.Error())}
	}

	// Inject traceparent if active
	if active := GetActiveTrace(); active != nil {
		req.Header.Set("traceparent", Traceparent(active))
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		MetricInc("http_client_errors_total")
		endSpan(0)
		return [2]interface{}{nil, fmt.Sprintf("HTTP GET request failed for %s: %s", url, err.Error())}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	duration := time.Since(start).Seconds()
	MetricGauge("http_client_request_duration_seconds", duration)

	endSpan(resp.StatusCode)
	return HTTPResponse{Status: resp.StatusCode, Body: string(body)}
}

func HTTPPost(url string, body interface{}) interface{} {
	endSpan := TraceHTTPClient("POST", url)
	start := time.Now()
	MetricInc("http_client_requests_total")

	var buf bytes.Buffer
	if strBody, ok := body.(string); ok {
		buf.WriteString(strBody)
	} else {
		json.NewEncoder(&buf).Encode(body)
	}

	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		MetricInc("http_client_errors_total")
		endSpan(0)
		return [2]interface{}{nil, fmt.Sprintf("HTTP POST request failed for %s: %s", url, err.Error())}
	}
	req.Header.Set("Content-Type", "application/json")

	// Inject traceparent if active
	if active := GetActiveTrace(); active != nil {
		req.Header.Set("traceparent", Traceparent(active))
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		MetricInc("http_client_errors_total")
		endSpan(0)
		return [2]interface{}{nil, fmt.Sprintf("HTTP POST request failed for %s: %s", url, err.Error())}
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)

	duration := time.Since(start).Seconds()
	MetricGauge("http_client_request_duration_seconds", duration)

	endSpan(resp.StatusCode)
	return HTTPResponse{Status: resp.StatusCode, Body: string(bodyBytes)}
}

// HTTPGetSafe is kept for backward compatibility — now just calls HTTPGet directly.
func HTTPGetSafe(url string) interface{} {
	return HTTPGet(url)
}

// HTTPPostSafe is kept for backward compatibility — now just calls HTTPPost directly.
func HTTPPostSafe(url string, body interface{}) interface{} {
	return HTTPPost(url, body)
}

// JSON native support
func JSONParse(dataVal interface{}) interface{} {
	data := fmt.Sprint(dataVal)
	var val interface{}
	err := json.Unmarshal([]byte(data), &val)
	if err != nil {
		return [2]interface{}{nil, fmt.Sprintf("JSON parse error: %s", err.Error())}
	}
	return ToSafeValue(val)
}

func JSONStringify(val interface{}) string {
	b, err := json.Marshal(val)
	if err != nil {
		return ""
	}
	return string(b)
}

// JSONParseSafe is kept for backward compatibility — now just calls JSONParse directly.
func JSONParseSafe(dataVal interface{}) interface{} {
	return JSONParse(dataVal)
}

// Registry — generic named function map for dynamic dispatch.
// Supports registering functions by name and calling them dynamically.
// Use cases: job schedulers, event handlers, plugin systems, command dispatch.

var (
	registryFuncs   = make(map[string]interface{})
	registryFuncsMu sync.RWMutex
)

// RegistrySet registers a function by name.
// Usage: registry.set("batch_processing", executeBatchProcessing)
func RegistrySet(name interface{}, handler interface{}) interface{} {
	key := fmt.Sprint(name)
	registryFuncsMu.Lock()
	registryFuncs[key] = handler
	registryFuncsMu.Unlock()
	LogInfo("Registry: registered handler '", key, "'")
	return nil
}

// RegistryCall invokes a registered function by name with the given arguments.
// Usage: registry.call("batch_processing", payload, idempotencyKey)
func RegistryCall(name interface{}, args ...interface{}) interface{} {
	key := fmt.Sprint(name)
	registryFuncsMu.RLock()
	handler, exists := registryFuncs[key]
	registryFuncsMu.RUnlock()

	if !exists {
		LogError("Registry: handler not found: '", key, "'")
		return nil
	}

	// Call the handler based on its type
	switch fn := handler.(type) {
	case func(interface{}) interface{}:
		if len(args) >= 1 {
			return fn(args[0])
		}
		return fn(nil)
	case func(interface{}, interface{}) interface{}:
		var a, b interface{}
		if len(args) >= 1 {
			a = args[0]
		}
		if len(args) >= 2 {
			b = args[1]
		}
		return fn(a, b)
	case func(interface{}, interface{}, interface{}) interface{}:
		var a, b, c interface{}
		if len(args) >= 1 {
			a = args[0]
		}
		if len(args) >= 2 {
			b = args[1]
		}
		if len(args) >= 3 {
			c = args[2]
		}
		return fn(a, b, c)
	default:
		LogError("Registry: handler '", key, "' has unsupported signature")
		return nil
	}
}

// RegistryList returns all registered handler names.
// Usage: let handlers = registry.list()
func RegistryList() interface{} {
	registryFuncsMu.RLock()
	defer registryFuncsMu.RUnlock()
	names := make([]interface{}, 0, len(registryFuncs))
	for k := range registryFuncs {
		names = append(names, k)
	}
	return names
}

// RegistryHas checks if a handler is registered.
// Usage: let exists = registry.has("batch_processing")
func RegistryHas(name interface{}) interface{} {
	key := fmt.Sprint(name)
	registryFuncsMu.RLock()
	_, exists := registryFuncs[key]
	registryFuncsMu.RUnlock()
	return exists
}
