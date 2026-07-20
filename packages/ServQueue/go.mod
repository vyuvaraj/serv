module github.com/vyuvaraj/serv/packages/ServQueue

go 1.25.0

require github.com/tetratelabs/wazero v1.12.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/vyuvaraj/serv/packages/ServShared v0.0.0
)

require (
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	golang.org/x/sys v0.44.0 // indirect
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
