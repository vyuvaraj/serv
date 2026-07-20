module servauth

go 1.25.0

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/vyuvaraj/ServShared v1.0.2-0.20260719054743-81a270f75198
	golang.org/x/crypto v0.53.0
)

replace github.com/vyuvaraj/ServShared => ../ServShared
