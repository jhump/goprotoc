# Go-protoc
[![Build Status](https://github.com/jhump/goprotoc/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/jhump/goprotoc/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jhump/goprotoc)](https://goreportcard.com/report/github.com/jhump/goprotoc)
[![GoDoc](https://godoc.org/github.com/jhump/goprotoc/plugins?status.svg)](https://godoc.org/github.com/jhump/goprotoc/plugins)

This repo makes it easy to work in the protobuf tool chain using Go. 

## Writing Plugins for `protoc`
First and foremost, the included `plugins` package makes it easy to implement `protoc` plugins in Go. It defines
an interface that plugins implement as well as facilities to actually integrate with `protoc` (e.g. implementing
the proper plugin protocol). It also provides "name resolution" logic: computing qualified names in Go source
code for elements in proto descriptors. This makes it a snap to write plugins in Go that generate additional Go
code from your proto sources.

## Pure Go version of `protoc`
This repo also contains a pure-Go re-implementation of `protoc`. This new version of `protoc`, named `goprotoc`
(of course!), will delegate to a `protoc` executable on the path, driving it as if it were a plugin, for generating
C++, C#, Objective-C, Java, JavaScript, Python, PHP, and Ruby code (since they are implemented in `protoc` itself).
But it provides descriptors to `protoc`, parsed by `goprotoc`, instead of having `protoc` re-parse all of the source
code. And it can invoke any other plugins (such as `protoc-gen-go`) the same way that `protoc` would.

In addition to the `goprotoc` command, this repo provides a package that other Go programs can use as the
entry-point to running Protocol Buffer code gen, without having to shell out to an external program.

## Extras
You'll also find a `protoc` plugin named `protoc-gen-gox` that can be the entry point for generating Go code. It
will delegate to `protoc-gen-go` for standard code gen and gRPC code gen, but it can also be configured to execute
other plugins that emit additional Go code. It's sort of like a plugin multiplexer that supports a configuration
file for enabling and configuring the various plugins that it invokes.
