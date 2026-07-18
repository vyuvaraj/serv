//go:build wasm

package runtime

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var SrvSourceMap map[string]int

// Request represents an HTTP request (stub for WASM target).
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    string            `json:"body"`
	Params  map[string]string `json:"params"`
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
}

// HTTPResponse represents an HTTP response.
type HTTPResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// WSConn represents a WebSocket connection (stub for WASM target).
type WSConn struct{}

func (w *WSConn) Send(msg interface{}) {}
func (w *WSConn) Receive() interface{} { return nil }
func (w *WSConn) Close()              {}

// ── Logging Module ───────────────────────────────────────────────────────────

var (
	logLevel   = "info"
	logLevelMu sync.RWMutex
	secrets    []string
	secretsMu  sync.RWMutex
)

func RegisterSecret(val string) {
	if val == "" {
		return
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	for _, s := range secrets {
		if s == val {
			return
		}
	}
	secrets = append(secrets, val)
}

func sanitizeLog(msg string) string {
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	for _, secret := range secrets {
		msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
	}
	return msg
}

func shouldLog(level string) bool {
	levels := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	logLevelMu.RLock()
	defer logLevelMu.RUnlock()
	return levels[level] >= levels[logLevel]
}

func logStructured(level string, args ...interface{}) {
	if !shouldLog(level) {
		return
	}
	msg := sanitizeLog(fmt.Sprint(args...))
	fmt.Fprintf(os.Stderr, "[%s] %s\n", strings.ToUpper(level), msg)
}

func logStructuredWithFields(level string, fields map[string]interface{}, args ...interface{}) {
	if !shouldLog(level) {
		return
	}
	msg := sanitizeLog(fmt.Sprint(args...))
	if len(fields) > 0 {
		var pairs []string
		for k, v := range fields {
			valStr := fmt.Sprint(v)
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, sanitizeLog(valStr)))
		}
		fmt.Fprintf(os.Stderr, "[%s] %s %s\n", strings.ToUpper(level), msg, strings.Join(pairs, " "))
	} else {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", strings.ToUpper(level), msg)
	}
}

func LogInfo(args ...interface{})  { logStructured("info", args...) }
func LogWarn(args ...interface{})  { logStructured("warn", args...) }
func LogError(args ...interface{}) { logStructured("error", args...) }
func LogDebug(args ...interface{}) { logStructured("debug", args...) }

type ContextLogger struct {
	Fields map[string]interface{}
}

func (cl *ContextLogger) Info(args ...interface{}) interface{} {
	logStructuredWithFields("info", cl.Fields, args...)
	return nil
}
func (cl *ContextLogger) Warn(args ...interface{}) interface{} {
	logStructuredWithFields("warn", cl.Fields, args...)
	return nil
}
func (cl *ContextLogger) Error(args ...interface{}) interface{} {
	logStructuredWithFields("error", cl.Fields, args...)
	return nil
}
func (cl *ContextLogger) Debug(args ...interface{}) interface{} {
	logStructuredWithFields("debug", cl.Fields, args...)
	return nil
}
func (cl *ContextLogger) With(args ...interface{}) *ContextLogger {
	merged := make(map[string]interface{})
	for k, v := range cl.Fields {
		merged[k] = v
	}
	for i := 0; i+1 < len(args); i += 2 {
		merged[fmt.Sprint(args[i])] = args[i+1]
	}
	return &ContextLogger{Fields: merged}
}

func LogWith(args ...interface{}) interface{} {
	fields := make(map[string]interface{})
	for i := 0; i+1 < len(args); i += 2 {
		fields[fmt.Sprint(args[i])] = args[i+1]
	}
	if len(args)%2 == 1 {
		msg := fmt.Sprint(args[len(args)-1])
		logStructuredWithFields("info", fields, msg)
		return nil
	}
	return &ContextLogger{Fields: fields}
}

func LogFields(args ...interface{}) interface{} {
	fields := make(map[string]interface{})
	if len(args) == 1 {
		switch m := args[0].(type) {
		case map[string]interface{}:
			fields = m
		case *SafeMap:
			for k, v := range m.All() {
				fields[k] = v
			}
		}
	}
	return &ContextLogger{Fields: fields}
}

func LogSetLevel(args ...interface{}) interface{} {
	if len(args) == 0 {
		return nil
	}
	lvl := strings.ToLower(fmt.Sprint(args[0]))
	switch lvl {
	case "debug", "info", "warn", "error":
		logLevelMu.Lock()
		logLevel = lvl
		logLevelMu.Unlock()
	}
	return nil
}

func LogGetLevel(args ...interface{}) interface{} {
	logLevelMu.RLock()
	defer logLevelMu.RUnlock()
	return logLevel
}

func ContextLoggerInfo(logger interface{}, args ...interface{}) interface{} {
	if cl, ok := logger.(*ContextLogger); ok {
		return cl.Info(args...)
	}
	logStructured("info", args...)
	return nil
}
func ContextLoggerWarn(logger interface{}, args ...interface{}) interface{} {
	if cl, ok := logger.(*ContextLogger); ok {
		return cl.Warn(args...)
	}
	logStructured("warn", args...)
	return nil
}
func ContextLoggerError(logger interface{}, args ...interface{}) interface{} {
	if cl, ok := logger.(*ContextLogger); ok {
		return cl.Error(args...)
	}
	logStructured("error", args...)
	return nil
}
func ContextLoggerDebug(logger interface{}, args ...interface{}) interface{} {
	if cl, ok := logger.(*ContextLogger); ok {
		return cl.Debug(args...)
	}
	logStructured("debug", args...)
	return nil
}
func ContextLoggerWith(logger interface{}, args ...interface{}) interface{} {
	if cl, ok := logger.(*ContextLogger); ok {
		return cl.With(args...)
	}
	return LogWith(args...)
}

// ── HTTP Client Stubs ────────────────────────────────────────────────────────

func HTTPGet(url string, headersVal ...interface{}) interface{} {
	return [2]interface{}{nil, "HTTPGet is not supported in the sandboxed WebAssembly target"}
}
func HTTPPost(url string, body interface{}, headersVal ...interface{}) interface{} {
	return [2]interface{}{nil, "HTTPPost is not supported in the sandboxed WebAssembly target"}
}
func HTTPGetSafe(url string, headersVal ...interface{}) interface{} { return HTTPGet(url, headersVal...) }
func HTTPPostSafe(url string, body interface{}, headersVal ...interface{}) interface{} { return HTTPPost(url, body, headersVal...) }

// ── Registry Stubs ───────────────────────────────────────────────────────────

var (
	registryFuncs   = make(map[string]interface{})
	registryFuncsMu sync.RWMutex
)

func RegistrySet(name interface{}, handler interface{}) interface{} {
	key := fmt.Sprint(name)
	registryFuncsMu.Lock()
	registryFuncs[key] = handler
	registryFuncsMu.Unlock()
	return nil
}
func RegistryCall(name interface{}, args ...interface{}) interface{} {
	key := fmt.Sprint(name)
	registryFuncsMu.RLock()
	handler, exists := registryFuncs[key]
	registryFuncsMu.RUnlock()
	if !exists {
		return nil
	}
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
	}
	return nil
}
func RegistryList() interface{} {
	registryFuncsMu.RLock()
	defer registryFuncsMu.RUnlock()
	names := make([]interface{}, 0, len(registryFuncs))
	for k := range registryFuncs {
		names = append(names, k)
	}
	return names
}
func RegistryHas(name interface{}) interface{} {
	key := fmt.Sprint(name)
	registryFuncsMu.RLock()
	_, exists := registryFuncs[key]
	registryFuncsMu.RUnlock()
	return exists
}

func Env(key string) string    { return os.Getenv(key) }
func EnvSecret(key string) string {
	val := os.Getenv(key)
	RegisterSecret(val)
	return val
}
func Config(key string) string { return "" }

// ── DB / Cache / Broker Stubs ────────────────────────────────────────────────

func InitDB(connStr string)     {}
func InitCache(connStr string)  {}
func InitBroker(connStr string) {}
func InitAI(connStr string)     {}
func AddAgent(name, system, model string, tools []string) {}
func AIComplete(args ...interface{}) interface{} { return nil }
func AIClassify(args ...interface{}) interface{} { return nil }
func AIChat(args ...interface{}) interface{} { return nil }
func AIEmbed(args ...interface{}) interface{} { return nil }

func CacheGet(key interface{}) interface{} {
	return [2]interface{}{nil, "Cache is not supported in the sandboxed WebAssembly target"}
}
func CacheSet(key, val interface{}, args ...interface{}) interface{} {
	return [2]interface{}{nil, "Cache is not supported in the sandboxed WebAssembly target"}
}

// ── Scheduler Stubs ──────────────────────────────────────────────────────────

func Every(intervalStr string, callback func()) {}
func Cron(cronExpr string, callback func())     {}
func Sleep(ms interface{}) interface{} {
	var duration time.Duration
	switch val := ms.(type) {
	case int:
		duration = time.Duration(val) * time.Millisecond
	case int64:
		duration = time.Duration(val) * time.Millisecond
	case float64:
		duration = time.Duration(val) * time.Millisecond
	}
	time.Sleep(duration)
	return nil
}
func CronNext(cronExpr interface{}) interface{}             { return "" }
func CronSleepUntilNext(cronExpr interface{}) interface{}   { return nil }
func SpawnWithTimeout(timeoutMs interface{}, fn func() interface{}) interface{} {
	return fn()
}

// ── S3 Stubs ─────────────────────────────────────────────────────────────────

func S3Init(endpointVal, accessKeyVal, secretKeyVal interface{}) interface{} { return nil }
func S3Put(bucketVal, keyVal, data interface{}) interface{}                 { return nil }
func S3Get(bucketVal, keyVal interface{}) interface{}                       { return nil }
func S3Delete(bucketVal, keyVal interface{}) interface{}                    { return nil }
func S3List(bucketVal interface{}, args ...interface{}) interface{}          { return nil }
func S3At(bucketVal, keyVal, timestampVal interface{}) interface{}          { return nil }
func S3Search(bucketVal, queryVal interface{}, args ...interface{}) interface{} { return nil }
func S3CreateBucket(bucketVal interface{}) interface{}                       { return nil }
func S3DeleteBucket(bucketVal interface{}) interface{}                       { return nil }
func S3SetBucketVersioning(bucketVal, statusVal interface{}) interface{}     { return nil }

// ── Service API Stubs ────────────────────────────────────────────────────────

func InitServer(port string)                          {}
func InitServerTLS(port, certFile, keyFile string)    {}
func StartServer() interface{}                        { return nil }
func CallPython(scriptPath string, funcName string, args ...interface{}) interface{} {
	return [2]interface{}{nil, "Python execution is not supported in the sandboxed WebAssembly target"}
}
func AddWebSocket(path string, handler func(*WSConn)) {}
func ValidateConfig(requiredKeys []string)            {}
func ValidateBody(args ...interface{}) interface{}    { return nil }

func RegisterMiddleware(name string, handler func(Request) interface{}) {}
func AddRoute(method, path string, rate int, period string, handler func(Request) interface{}) {}
func AddRouteWithMiddleware(method, path string, rate int, period string, middleware []string, handler func(Request) interface{}) {
}
func AddStaticRoute(prefix, dir string) {}
func AddMCPTool(name, description string, handler func(interface{}) interface{}) {}
func RegisterMigration(name string, handler func() interface{}) {}
func RegisterDBSchema(schemaJSON string)                        {}
func RegisterTableSchema(tableName, createSQL string)           {}
func GetTableSchemas() map[string]string                        { return nil }
func DBExec(sql string) interface{}                             { return nil }
func Subscribe(topic string, handler func(string))              {}
func Publish(topic string, val interface{}) interface{}        { return nil }

// ── OTEL & Concurrency / Semaphore Stubs ──────────────────────────────────────

func AcquireSemaphore(name string, fn func() int) interface{} { return fn() }
func ReleaseSemaphore(name string) {}

// RequestTrace WASM stub matches the non-wasm struct so cross-target code
// (e.g. otel_test.go) can access .TraceID without type assertions.
type RequestTrace struct {
	TraceID  string
	SpanID   string
	ParentID string
	Method   string
	Path     string
}

func SetActiveTrace(trace *RequestTrace)                            {}
func ClearActiveTrace()                                             {}
func GetActiveTrace() *RequestTrace                                 { return nil }
func TraceSpawn(name string) func()                                 { return func() {} }
func Traceparent(trace *RequestTrace) string                        { return "" }
func OtelEnabled() bool                                             { return false }
func TraceRequest(method, path string, parentTrace string) *RequestTrace { return &RequestTrace{} }
func EndTrace(rt *RequestTrace, statusCode int)                     {}
func TraceDB(operation, query string) func()                        { return func() {} }
func TraceCache(operation, key string) func()                       { return func() {} }
func TraceHTTPClient(method, url string) func(statusCode int)       { return func(int) {} }
func TracePubSub(operation, topic string) func()                    { return func() {} }
func TraceScheduler(jobType, schedule string) func()                { return func() {} }
func TraceWebSocket(path, event string) func()                      { return func() {} }
func TraceExtern(source, funcName string) func()                    { return func() {} }

func ResetDBState() {}
func ClearCache()   {}

func HTMLTemplate(tpl string, data interface{}) interface{} {
	return "HTMLTemplate is not supported in the WebAssembly target"
}

func HTMLRender(filePath string, data interface{}) interface{} {
	return "HTMLRender is not supported in the WebAssembly target"
}

func HTMLRedirect(url interface{}, code interface{}) interface{} { return nil }
func ParseFormBody(body string) interface{}                       { return map[string]interface{}{} }
func RequestParam(req Request, key interface{}) interface{}       { return nil }


// ── Metrics Stubs ────────────────────────────────────────────────────────────

// ── Exec and File Stubs ───────────────────────────────────────────────────────

func ExecRun(cmdStr string) interface{} {
	return map[string]interface{}{
		"stdout":   "",
		"stderr":   "ExecRun is not supported in the sandboxed WebAssembly target",
		"exitCode": float64(-1),
	}
}

func FileRead(path string) interface{} {
	return [2]interface{}{nil, "FileRead is not supported in the sandboxed WebAssembly target"}
}

func FileWrite(path string, content string) interface{} {
	return [2]interface{}{nil, "FileWrite is not supported in the sandboxed WebAssembly target"}
}

func FileExists(path string) interface{} {
	return false
}

func FileList(path string) interface{} {
	return [2]interface{}{nil, "FileList is not supported in the sandboxed WebAssembly target"}
}

// ── Utility Namespaces Stubs ──────────────────────────────────────────────────

func CSVParse(content string) interface{} {
	return [2]interface{}{nil, "CSVParse is not supported in the sandboxed WebAssembly target"}
}

func CSVStringify(rows interface{}, headers interface{}) interface{} {
	return [2]interface{}{nil, "CSVStringify is not supported in the sandboxed WebAssembly target"}
}

func XMLParse(content string) interface{} {
	return [2]interface{}{nil, "XMLParse is not supported in the sandboxed WebAssembly target"}
}

func XMLStringify(obj interface{}) interface{} {
	return [2]interface{}{nil, "XMLStringify is not supported in the sandboxed WebAssembly target"}
}

func YAMLParse(content string) interface{} {
	return [2]interface{}{nil, "YAMLParse is not supported in the sandboxed WebAssembly target"}
}

func YAMLStringify(obj interface{}) interface{} {
	return [2]interface{}{nil, "YAMLStringify is not supported in the sandboxed WebAssembly target"}
}

func PathJoin(args ...interface{}) interface{} {
	return ""
}

func PathDirname(path string) interface{} {
	return "."
}

func PathBasename(path string) interface{} {
	return path
}

func PathExt(path string) interface{} {
	return ""
}

func PathAbs(path string) interface{} {
	return [2]interface{}{nil, "PathAbs is not supported in the sandboxed WebAssembly target"}
}

// ── Regex, Math, Encoding, and Hash Stubs ──────────────────────────────────────

func RegexMatch(pattern interface{}, value interface{}) interface{} {
	return false
}

func RegexFind(pattern interface{}, value interface{}) interface{} {
	return ""
}

func RegexReplace(pattern interface{}, value interface{}, replacement interface{}) interface{} {
	return value
}

func MathFloor(x interface{}) interface{} {
	return 0.0
}

func MathCeil(x interface{}) interface{} {
	return 0.0
}

func MathRound(x interface{}) interface{} {
	return 0.0
}

func MathAbs(x interface{}) interface{} {
	return 0.0
}

func MathPow(base, exp interface{}) interface{} {
	return 0.0
}

func MathSqrt(x interface{}) interface{} {
	return 0.0
}

func MathMin(a, b interface{}) interface{} {
	return a
}

func MathMax(a, b interface{}) interface{} {
	return b
}

type EncodingBase64Namespace struct{}
type EncodingHexNamespace struct{}

func Base64Encode(str interface{}) interface{} {
	return ""
}

func Base64Decode(str interface{}) interface{} {
	return [2]interface{}{nil, "Base64Decode is not supported in the sandboxed WebAssembly target"}
}

func HexEncode(str interface{}) interface{} {
	return ""
}

func HexDecode(str interface{}) interface{} {
	return [2]interface{}{nil, "HexDecode is not supported in the sandboxed WebAssembly target"}
}

func HashMD5(str interface{}) interface{} {
	return ""
}

func HashSHA256(str interface{}) interface{} {
	return ""
}

func HashSHA512(str interface{}) interface{} {
	return ""
}

func HashHMAC(key interface{}, data interface{}, algo interface{}) interface{} {
	return [2]interface{}{nil, "HashHMAC is not supported in the sandboxed WebAssembly target"}
}

// ── UUID, Rand, URL, and Env Stubs ───────────────────────────────────────────

func UUIDv4() interface{} {
	return ""
}

func UUIDv7() interface{} {
	return [2]interface{}{nil, "UUIDv7 is not supported in the sandboxed WebAssembly target"}
}

func RandInt(min, max interface{}) interface{} {
	return min
}

func RandFloat() interface{} {
	return 0.0
}

func RandString(n interface{}) interface{} {
	return ""
}

func RandBool() interface{} {
	return false
}

func URLParse(urlStr interface{}) interface{} {
	return [2]interface{}{nil, "URLParse is not supported in the sandboxed WebAssembly target"}
}

func URLEncode(str interface{}) interface{} {
	return ""
}

func URLDecode(str interface{}) interface{} {
	return [2]interface{}{nil, "URLDecode is not supported in the sandboxed WebAssembly target"}
}

func EnvGet(key interface{}) interface{} {
	return ""
}

func EnvRequire(key interface{}) interface{} {
	panic("EnvRequire is not supported in the sandboxed WebAssembly target")
}

func EnvInt(key interface{}, defaultValue interface{}) interface{} {
	return defaultValue
}

func EnvBool(key interface{}, defaultValue interface{}) interface{} {
	return defaultValue
}

// ── Time Overhaul Stubs ───────────────────────────────────────────────────────

func TimeParse(str, layout interface{}) interface{} {
	return [2]interface{}{nil, "TimeParse is not supported in the sandboxed WebAssembly target"}
}

func TimeFormat(tVal, layout interface{}) interface{} {
	return ""
}

func TimeInZone(tVal, tz interface{}) interface{} {
	return tVal
}

func TimeUTC(tVal interface{}) interface{} {
	return tVal
}

func TimeLocal(tVal interface{}) interface{} {
	return tVal
}

func TimeAdd(tVal, durVal interface{}) interface{} {
	return tVal
}

func TimeSub(t1Val, t2Val interface{}) interface{} {
	return 0.0
}

func TimeBefore(t1Val, t2Val interface{}) interface{} {
	return false
}

func TimeAfter(t1Val, t2Val interface{}) interface{} {
	return false
}

func TimeFromUnix(seconds interface{}) interface{} {
	return ""
}

func TimeComponents(tVal interface{}) interface{} {
	return map[string]interface{}{}
}

// ── JWT Stubs ───────────────────────────────────────────────────────────────

func JWTSign(payload, secret interface{}) interface{} {
	return ""
}

func JWTVerify(token, secret interface{}) interface{} {
	return map[string]interface{}{}
}

func JWTDecode(token interface{}) interface{} {
	return map[string]interface{}{}
}

// ── Compress Stubs ──────────────────────────────────────────────────────────

func CompressGzip(data interface{}) interface{} {
	return []byte{}
}

func CompressUngzip(bytesVal interface{}) interface{} {
	return ""
}

func CompressDeflate(data interface{}) interface{} {
	return []byte{}
}

func CompressInflate(bytesVal interface{}) interface{} {
	return ""
}

// ── Semver Stubs ────────────────────────────────────────────────────────────

func SemverParse(version interface{}) interface{} {
	return map[string]interface{}{}
}

func SemverCompare(v1, v2 interface{}) interface{} {
	return 0.0
}

func SemverSatisfies(rangeStr, version interface{}) interface{} {
	return false
}

// ── Duration Stubs ──────────────────────────────────────────────────────────

func DurationParse(durStr interface{}) interface{} {
	return 0.0
}

func DurationFormat(seconds interface{}) interface{} {
	return ""
}

func DurationSince(ts interface{}) interface{} {
	return 0.0
}





