# Go-protoc
[![Build Status](https://circleci.com/gh/jhump/goprotoc/tree/master.svg?style=svg)](https://circleci.com/gh/jhump/goprotoc/tree/master)
[![Go Report Card](https://goreportcard.com/badge/github.com/jhump/goprotoc)](https://goreportcard.com/report/github.com/jhump/goprotoc)
[![GoDoc](https://godoc.org/github.com/jhump/goprotoc/plugins?status.svg)](https://godoc.org/github.com/jhump/goprotoc/plugins)

This repo makes it easy to build Protobuf plugins. The official Protobuf runtime for Go includes a similar
package, but it is in practice only useful for generating Go code. It places extra constraints on the input
files that only make sense when generating Go code, and it uses a model that includes several Go-specific
fields (like the Go package and import path for a file, or the Go name for an element). This has no such
constraints and uses a simpler model (`protoreflect.Descriptor`) to provide an easy way to build plugins
that generate code other than Go.

## Status: Experimental

The V1 of this repo included additional APIs that are useful for generating Go code. It is effectively
superseded by the newer [`google.golang.org/protobuf/compiler/protogen`](https://pkg.go.dev/google.golang.org/protobuf/compiler/protogen)
package that is now part of the Protobuf runtime for Go. The V1 also included a `goprotoc` binary but
that was just an experiment that never reached parity with the official `protoc` and has not been
maintained. If you're looking for a `protoc` replacement, take a look at [`buf`](https://github.com/bufbuild/buf).

What remains in this V2 may never see the light of day in a release. It is a paired down and updated
versions of the V1 `plugins` package. It is experimental.
