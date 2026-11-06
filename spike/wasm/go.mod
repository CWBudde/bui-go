module github.com/CWBudde/bui-go/spike/wasm

go 1.26

replace github.com/CWBudde/bui-go => ../..

require (
	github.com/CWBudde/bui-go v0.0.0-00010101000000-000000000000
	github.com/tetratelabs/wazero v1.12.0
)

require golang.org/x/sys v0.44.0 // indirect
