// This module is only used by the goprotoc tests. It declares a protoc plugin
// as a tool so that the tests can verify that goprotoc finds and runs it via
// "go tool", using the version recorded in this go.mod.
module example.com/gotooltest

go 1.24

tool example.com/gotooltest/protoc-gen-testplugin
