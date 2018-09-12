# Go-protoc

## Still Under Construction!

This library makes it easy to implement `protoc` plugins in Go. It includes an interface that plugins implement
as well as libraries to take care of integration with `protoc` (e.g. implementing the proper plugin protocol) and
to provide "name resolution" logic: computing qualified names in Go source code for elements in proto descriptors.

It also includes a `protoc` plugin named `protoc-gen-gox` that can be the entry point for generating Go code. It
will delegate to `protoc-gen-go` for standard code gen and gRPC code gen, but it can also be configured to execute
other plugins that emit additional Go code.

As an interesting exercise, it also contains a pure-Go re-implementation of `protoc`. This new version of `protoc`
will delegate to a `protoc` executable on the path, driving it as if it were a plugin, for generating C++, C#,
Objective-C, Java, JavaScript, Python, PHP, and Ruby code (since they are implemented in `protoc` itself). But it
provides descriptors to `protoc`, parsed by `goprotoc`, instead of having `protoc` re-parse all of the source code.
