package config

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"
	"sync"
)

type ServDiscovery struct {
	Gate         string `json:"gate"`
	Store        string `json:"store"`
	Queue        string `json:"queue"`
	Trace        string `json:"trace"`
	Tunnel       string `json:"tunnel"`
	Auth         string `json:"auth"`
	DB           string `json:"db"`
	Mail         string `json:"mail"`
	Flow         string `json:"flow"`
	Mesh         string `json:"mesh"`
	Cron         string `json:"cron"`
	Cache        string `json:"cache"`
	Registry     string `json:"registry"`
	Cloud        string `json:"cloud"`
	Docs         string `json:"docs"`
	Lock         string `json:"lock"`
	Secret       string `json:"secret"`
	ConsolePort  int    `json:"console_port"`
	JWTSecret    string `json:"jwt_secret"`
	OTLPEndpoint string `json:"otlp_endpoint"`
	GateConfig   string `json:"gate_config"`
	AuthToken    string `json:"auth_token"`
}

type Route struct {
	Prefix             string   `json:"prefix"`
	Target             string   `json:"target"`
	Targets            []string `json:"targets,omitempty"`
	LoadBalancer       string   `json:"load_balancer,omitempty"`
	TranspileType      string   `json:"transpile_type,omitempty"`
	Middleware         string   `json:"middleware,omitempty"`
	ResponseMiddleware string   `json:"response_middleware,omitempty"`
	RateLimitRPM       int      `json:"rate_limit_rpm,omitempty"`
	PromptGuard        bool     `json:"prompt_guard,omitempty"`
	PiiRedact          bool     `json:"pii_redact,omitempty"`
	SemanticCache      bool     `json:"semantic_cache,omitempty"`
}

type GatewayConfig struct {
	Addr      string  `json:"addr"`
	AuthToken string  `json:"auth_token"`
	TlsCert   string  `json:"tls_cert"`
	TlsKey    string  `json:"tls_key"`
	Routes    []Route `json:"routes"`
	Signature string  `json:"signature,omitempty"`
}

type ComponentStatus struct {
	Name      string    `json:"name"`
	Online    bool      `json:"online"`
	Url       string    `json:"url"`
	LatencyMs int64     `json:"latency_ms"`
	Details   any       `json:"details,omitempty"`
}

var (
	Port        = flag.Int("port", 8083, "Port to listen on")
	GateUrl     = flag.String("gate-url", "http://localhost:8080", "ServGate base URL")
	StoreUrl    = flag.String("store-url", "http://localhost:8081", "ServStore base URL")
	QueueUrl    = flag.String("queue-url", "http://localhost:8082", "ServQueue base URL")
	TraceUrl    = flag.String("trace-url", "http://localhost:8090", "ServTrace base URL")
	TunnelUrl   = flag.String("tunnel-url", "http://localhost:8443", "ServTunnel base URL")
	AuthUrl     = flag.String("auth-url", "http://localhost:8098", "ServAuth base URL")
	DbUrl       = flag.String("db-url", "http://localhost:8097", "ServDB base URL")
	MailUrl     = flag.String("mail-url", "http://localhost:8094", "ServMail base URL")
	FlowUrl     = flag.String("flow-url", "http://localhost:8096", "ServFlow base URL")
	MeshUrl     = flag.String("mesh-url", "http://localhost:8089", "ServMesh base URL")
	CronUrl     = flag.String("cron-url", "http://localhost:8087", "ServCron base URL")
	CacheUrl    = flag.String("cache-url", "http://localhost:8086", "ServCache base URL")
	RegistryUrl = flag.String("registry-url", "http://localhost:8088", "ServRegistry base URL")
	CloudUrl    = flag.String("cloud-url", "http://localhost:8085", "ServCloud base URL")
	DocsUrl     = flag.String("docs-url", "http://localhost:8084", "ServDocs base URL")
	LockUrl     = flag.String("lock-url", "http://localhost:8089", "ServLock base URL")
	SecretUrl   = flag.String("secret-url", "http://localhost:8091", "ServSecret base URL")
	AuthToken   = flag.String("auth-token", "gateway-secret-token", "Default API Auth token to use for downstream proxying")
	GateConfig  = flag.String("gate-config", "../ServGate/config.json", "Path to ServGate config.json")
	StartAll    = flag.Bool("start-all", false, "Boot all ecosystem services in dependency order on startup")

	ActiveDiscovery ServDiscovery
	ConfigMu        sync.Mutex
)

func LoadDiscovery() ServDiscovery {
	d := ServDiscovery{
		Gate:         *GateUrl,
		Store:        *StoreUrl,
		Queue:        *QueueUrl,
		Trace:        *TraceUrl,
		Tunnel:       *TunnelUrl,
		Auth:         *AuthUrl,
		DB:           *DbUrl,
		Mail:         *MailUrl,
		Flow:         *FlowUrl,
		Mesh:         *MeshUrl,
		Cron:         *CronUrl,
		Cache:        *CacheUrl,
		Registry:     *RegistryUrl,
		Cloud:        *CloudUrl,
		Docs:         *DocsUrl,
		Lock:         *LockUrl,
		Secret:       *SecretUrl,
		ConsolePort:  *Port,
		AuthToken:    *AuthToken,
		GateConfig:   *GateConfig,
		OTLPEndpoint: os.Getenv("SERV_OTLP_ENDPOINT"),
		JWTSecret:    os.Getenv("SERV_JWT_SECRET"),
	}

	raw := os.Getenv("SERVVERSE_DISCOVERY")
	if raw == "" {
		log.Println("[discovery] SERVVERSE_DISCOVERY not set — using CLI flags / defaults")
		return d
	}

	var manifest ServDiscovery
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		data, ferr := os.ReadFile(raw)
		if ferr != nil {
			log.Printf("[discovery] SERVVERSE_DISCOVERY is neither valid JSON nor a readable file: %v", ferr)
			return d
		}
		if err2 := json.Unmarshal(data, &manifest); err2 != nil {
			log.Printf("[discovery] Failed to parse discovery file %s: %v", raw, err2)
			return d
		}
		log.Printf("[discovery] Loaded from file: %s", raw)
	} else {
		log.Println("[discovery] Loaded from SERVVERSE_DISCOVERY env var (inline JSON)")
	}

	if manifest.Gate != "" { d.Gate = manifest.Gate }
	if manifest.Store != "" { d.Store = manifest.Store }
	if manifest.Queue != "" { d.Queue = manifest.Queue }
	if manifest.ConsolePort != 0 { d.ConsolePort = manifest.ConsolePort }
	if manifest.AuthToken != "" { d.AuthToken = manifest.AuthToken }
	if manifest.JWTSecret != "" { d.JWTSecret = manifest.JWTSecret }
	if manifest.OTLPEndpoint != "" { d.OTLPEndpoint = manifest.OTLPEndpoint }
	if manifest.GateConfig != "" { d.GateConfig = manifest.GateConfig }
	if manifest.Trace != "" { d.Trace = manifest.Trace }
	if manifest.Tunnel != "" { d.Tunnel = manifest.Tunnel }
	if manifest.Auth != "" { d.Auth = manifest.Auth }
	if manifest.DB != "" { d.DB = manifest.DB }
	if manifest.Mail != "" { d.Mail = manifest.Mail }
	if manifest.Flow != "" { d.Flow = manifest.Flow }
	if manifest.Mesh != "" { d.Mesh = manifest.Mesh }
	if manifest.Cron != "" { d.Cron = manifest.Cron }
	if manifest.Cache != "" { d.Cache = manifest.Cache }
	if manifest.Registry != "" { d.Registry = manifest.Registry }
	if manifest.Cloud != "" { d.Cloud = manifest.Cloud }
	if manifest.Docs != "" { d.Docs = manifest.Docs }
	if manifest.Lock != "" { d.Lock = manifest.Lock }
	if manifest.Secret != "" { d.Secret = manifest.Secret }

	return d
}

func Redact(s string) string {
	if len(s) <= 8 {
		return "********"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func DiscoverySource() string {
	raw := os.Getenv("SERVVERSE_DISCOVERY")
	if raw == "" {
		return "flags/defaults"
	}
	if strings.HasPrefix(raw, "{") {
		return "env_json"
	}
	return "file:" + raw
}
