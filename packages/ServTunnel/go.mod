module servtunnel

go 1.25.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/vyuvaraj/ServShared v1.0.2-0.20260719054743-81a270f75198
	golang.org/x/crypto v0.53.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/vyuvaraj/ServShared => ../ServShared
