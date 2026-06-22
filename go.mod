module servqueue

go 1.26.4

require github.com/tetratelabs/wazero v1.7.2

require github.com/vyuvaraj/ServShared v0.0.0

require github.com/golang-jwt/jwt/v5 v5.3.1 // indirect

replace github.com/vyuvaraj/ServShared => ../ServShared
