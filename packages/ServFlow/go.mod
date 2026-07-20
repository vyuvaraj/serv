module github.com/vyuvaraj/serv/packages/ServFlow

go 1.25.0

require (
	github.com/go-sql-driver/mysql v1.10.0
	github.com/go-stomp/stomp/v3 v3.1.5
	github.com/lib/pq v1.12.3
	github.com/vyuvaraj/serv/packages/ServShared v0.0.0
	modernc.org/sqlite v1.53.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.44.0 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/vyuvaraj/serv/packages/Serv-lang => ../Serv-lang

replace github.com/vyuvaraj/serv/packages/ServAuth => ../ServAuth

replace github.com/vyuvaraj/serv/packages/ServCache => ../ServCache

replace github.com/vyuvaraj/serv/packages/ServCloud => ../ServCloud

replace github.com/vyuvaraj/serv/packages/ServConsole => ../ServConsole

replace github.com/vyuvaraj/serv/packages/ServCron => ../ServCron

replace github.com/vyuvaraj/serv/packages/ServFlow => ../ServFlow

replace github.com/vyuvaraj/serv/packages/ServGate => ../ServGate

replace github.com/vyuvaraj/serv/packages/ServLock => ../ServLock

replace github.com/vyuvaraj/serv/packages/ServMail => ../ServMail

replace github.com/vyuvaraj/serv/packages/ServMesh => ../ServMesh

replace github.com/vyuvaraj/serv/packages/ServPool => ../ServPool

replace github.com/vyuvaraj/serv/packages/ServQueue => ../ServQueue

replace github.com/vyuvaraj/serv/packages/ServRegistry => ../ServRegistry

replace github.com/vyuvaraj/serv/packages/ServSecret => ../ServSecret

replace github.com/vyuvaraj/serv/packages/ServShared => ../ServShared

replace github.com/vyuvaraj/serv/packages/ServStore => ../ServStore

replace github.com/vyuvaraj/serv/packages/ServTrace => ../ServTrace

replace github.com/vyuvaraj/serv/packages/ServTunnel => ../ServTunnel
