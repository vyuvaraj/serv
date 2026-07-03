package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type TraceSummary struct {
	TraceID       string  `json:"traceId"`
	RootName      string  `json:"rootName"`
	Service       string  `json:"service"`
	DurationMs    float64 `json:"durationMs"`
	TotalSpans    int     `json:"totalSpans"`
	ErrorCount    int     `json:"errorCount"`
	TimestampNano int64   `json:"timestampUnixNano"`
}

type Span struct {
	TraceID      string                 `json:"traceId"`
	SpanID       string                 `json:"spanId"`
	ParentSpanID string                 `json:"parentSpanId,omitempty"`
	Name         string                 `json:"name"`
	Kind         int                    `json:"kind"`
	StartTime    int64                  `json:"startTimeUnixNano"`
	EndTime      int64                  `json:"endTimeUnixNano"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
	Status       int                    `json:"status"`
	Service      string                 `json:"service"`
}

type SpanNode struct {
	Span       Span        `json:"span"`
	Children   []*SpanNode `json:"children,omitempty"`
	DurationMs float64     `json:"durationMs"`
	OffsetMs   float64     `json:"offsetMs"`
}

func runTraceCmd() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: serv trace search [options] or serv trace inspect <trace_id>")
		os.Exit(1)
	}

	subCmd := os.Args[2]
	switch subCmd {
	case "search":
		searchTraces()
	case "inspect":
		if len(os.Args) < 4 {
			fmt.Println("Usage: serv trace inspect <trace_id>")
			os.Exit(1)
		}
		inspectTrace(os.Args[3])
	default:
		fmt.Printf("Unknown trace subcommand: %s. Use 'search' or 'inspect'.\n", subCmd)
		os.Exit(1)
	}
}

func searchTraces() {
	searchFlags := flag.NewFlagSet("trace search", flag.ExitOnError)
	service := searchFlags.String("service", "", "Filter by service name")
	operation := searchFlags.String("operation", "", "Filter by root span/operation name")
	errOnly := searchFlags.Bool("error", false, "Filter by traces containing errors")
	minDuration := searchFlags.Float64("min-duration", 0, "Filter by minimum duration in milliseconds")
	format := searchFlags.String("format", "text", "Output format: text, json, waterfall")
	host := searchFlags.String("host", "http://localhost:8090", "ServTrace collector host")

	_ = searchFlags.Parse(os.Args[3:])

	q := url.Values{}
	if *service != "" {
		q.Set("service", *service)
	}
	if *operation != "" {
		q.Set("operation", *operation)
	}
	if *errOnly {
		q.Set("error", "true")
	}
	if *minDuration > 0 {
		q.Set("min_duration_ms", fmt.Sprintf("%f", *minDuration))
	}

	apiURL := fmt.Sprintf("%s/api/traces", strings.TrimSuffix(*host, "/"))
	if len(q) > 0 {
		apiURL = apiURL + "?" + q.Encode()
	}

	req, _ := http.NewRequest("GET", apiURL, nil)
	authToken := os.Getenv("SERV_TRACE_AUTH_TOKEN")
	if authToken == "" {
		authToken = "secret-token"
	}
	req.Header.Set("Authorization", "Bearer "+authToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect to ServTrace collector: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("ServTrace returned error status %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	if *format == "json" {
		_, _ = io.Copy(os.Stdout, resp.Body)
		fmt.Println()
		return
	}

	var traces []TraceSummary
	if err := json.NewDecoder(resp.Body).Decode(&traces); err != nil {
		fmt.Printf("Failed to parse response: %v\n", err)
		os.Exit(1)
	}

	if len(traces) == 0 {
		fmt.Println("No matching traces found.")
		return
	}

	if *format == "waterfall" {
		inspectTraceWithHost(*host, traces[0].TraceID)
		return
	}

	// Print trace list sorted by timestamp desc
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].TimestampNano > traces[j].TimestampNano
	})

	fmt.Printf("%-34s %-20s %-25s %-12s %-8s %-6s\n", "TRACE ID", "SERVICE", "OPERATION", "DURATION", "SPANS", "ERRS")
	fmt.Println(strings.Repeat("-", 110))
	for _, t := range traces {
		tStr := time.Unix(0, t.TimestampNano).Format("15:04:05")
		durStr := fmt.Sprintf("%.2fms", t.DurationMs)
		fmt.Printf("%-34s %-20s %-25s %-12s %-8d %-6d (at %s)\n", t.TraceID, t.Service, t.RootName, durStr, t.TotalSpans, t.ErrorCount, tStr)
	}
}

func inspectTrace(traceID string) {
	host := "http://localhost:8090"
	if envHost := os.Getenv("SERV_TRACE_URL"); envHost != "" {
		host = envHost
	}
	inspectTraceWithHost(host, traceID)
}

func inspectTraceWithHost(host, traceID string) {
	apiURL := fmt.Sprintf("%s/api/traces/%s", strings.TrimSuffix(host, "/"), traceID)
	req, _ := http.NewRequest("GET", apiURL, nil)
	authToken := os.Getenv("SERV_TRACE_AUTH_TOKEN")
	if authToken == "" {
		authToken = "secret-token"
	}
	req.Header.Set("Authorization", "Bearer "+authToken)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect to ServTrace: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		fmt.Printf("Trace ID %s not found.\n", traceID)
		return
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("ServTrace returned error status %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var rootNode SpanNode
	if err := json.NewDecoder(resp.Body).Decode(&rootNode); err != nil {
		fmt.Printf("Failed to parse trace tree: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n=== Trace Waterfall: %s ===\n", traceID)
	renderWaterfall(&rootNode, 0, rootNode.DurationMs)
	fmt.Println()
}

func renderWaterfall(node *SpanNode, indent int, totalDuration float64) {
	if node == nil {
		return
	}

	errIndicator := ""
	if node.Span.Status == 2 {
		errIndicator = " ❌ [ERROR]"
	}
	label := fmt.Sprintf("%s -> %s%s", node.Span.Service, node.Span.Name, errIndicator)
	
	const barWidth = 40
	startPercent := node.OffsetMs / totalDuration
	durationPercent := node.DurationMs / totalDuration
	
	startChars := int(startPercent * barWidth)
	if startChars < 0 {
		startChars = 0
	}
	durationChars := int(durationPercent * barWidth)
	if durationChars <= 0 {
		durationChars = 1
	}
	if startChars+durationChars > barWidth {
		durationChars = barWidth - startChars
	}

	barSpace := strings.Repeat(" ", startChars)
	barBlocks := strings.Repeat("█", durationChars)
	barEmpty := strings.Repeat("░", barWidth-(startChars+durationChars))
	
	indentSpace := strings.Repeat("  ", indent)
	
	fmt.Printf("%-50s [%s%s%s] %.2fms (offset %.2fms)\n", 
		indentSpace+label, 
		barSpace, barBlocks, barEmpty, 
		node.DurationMs, node.OffsetMs)

	for _, child := range node.Children {
		renderWaterfall(child, indent+1, totalDuration)
	}
}
