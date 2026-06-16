package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
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

func getMetricKey(name string, labels ...interface{}) string {
	if len(labels) == 0 {
		return name
	}

	var labelMap map[string]interface{}
	first := labels[0]
	if sm, ok := first.(*SafeMap); ok {
		labelMap = sm.All()
	} else if m, ok := first.(map[string]interface{}); ok {
		labelMap = m
	}

	if len(labelMap) == 0 {
		return name
	}

	var keys []string
	for k := range labelMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, fmt.Sprint(labelMap[k])))
	}

	return fmt.Sprintf("%s{%s}", name, strings.Join(parts, ","))
}

func MetricInc(name string, labels ...interface{}) {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	key := getMetricKey(name, labels...)
	metricsCounters[key]++
}

func MetricGauge(name string, val float64, labels ...interface{}) {
	metricsGauges.Lock()
	defer metricsGauges.Unlock()
	key := getMetricKey(name, labels...)
	metricsGauges.m[key] = val
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

// WASM helpers for Serverless Compute
func WasmReadInput() interface{} {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return string(data)
}

func WasmWriteOutput(data interface{}) interface{} {
	str := fmt.Sprint(data)
	_, _ = os.Stdout.WriteString(str)
	return nil
}

// Noop is a no-op sentinel used by generated test files to satisfy the runtime import.
func Noop() {}
