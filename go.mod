module servcache

go 1.26.4

require (
	github.com/redis/go-redis/v9 v9.5.1
	github.com/vyuvaraj/ServShared v1.0.1
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
)
replace github.com/vyuvaraj/ServShared => ../ServShared
