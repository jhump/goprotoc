// Command protoc is 100% Go implementation of protoc. It can generate
// code by invoking other plugins, shelling out to external programs in
// the same way that the standard protoc does. It can also link in Go
// plugins that register protoc plugins via plugins.RegisterPlugin during
// their initialization. It aims to provide much of the same functionality
// as protoc, including the ability to read and write descriptors and to
// encode and decode files that contain text- or binary-encoded protocol
// buffer messages.
//
// Unlike the standard protoc, it does not provide any builtin code
// generation logic: it can only execute plugins to generate code. In order
// to generate code that is built into the standard protoc (such as Python,
// C++, Java, etc), this program can shell out to the standard protoc,
// driving it as if it were a plugin.
package main

import (
	"fmt"
	"os"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
)

const protocVersionEmu = "goprotoc 3.5.1"

var gitSha = "" // can be replaced by -X linker flag

func main() {
	var opts protocOptions
	err := parseFlags("", os.Args[1:], &opts, map[string]struct{}{})
	if err != nil {
		fail(err.Error())
	}

	if len(opts.inputDescriptors) > 0 && len(opts.includePaths) > 0 {
		fail("Only one of --descriptor_set_in and --proto_path can be specified.")
	}

	var fds []*desc.FileDescriptor
	if len(opts.inputDescriptors) > 0 {
		var err error
		if fds, err = loadDescriptors(opts.inputDescriptors); err != nil {
			fail(err.Error())
		}
	} else {
		p := protoparse.Parser{
			ImportPaths:           opts.includePaths,
			IncludeSourceCodeInfo: opts.includeSourceInfo,
		}
		var err error
		if fds, err = p.ParseFiles(opts.protoFiles...); err != nil {
			fail(err.Error())
		}
	}

	if len(opts.output) > 0 && opts.encodeType != "" {
		fail("Cannot use --encode and generate code or descriptors at the same time.")
	}
	if len(opts.output) > 0 && (opts.decodeType != "" || opts.decodeRaw) {
		fail("Cannot use --decode and generate code or descriptors at the same time.")
	}
	if opts.encodeType != "" && (opts.decodeType != "" || opts.decodeRaw) {
		fail("Only one of --encode and --decode can be specified.")
	}

	switch {
	case opts.encodeType != "":
		err = doEncode(opts.encodeType, fds, os.Stdin)
	case opts.decodeType != "":
		err = doDecode(opts.decodeType, fds, os.Stdin)
	case opts.decodeRaw:
		err = doDecodeRaw(fds, os.Stdin)
	case opts.printFreeFieldNumbers:
		err = doPrintFreeFieldNumbers(fds, os.Stdout)
	default:
		if len(opts.output) == 0 {
			fail("Missing output directives.")
		}
		err = doCodeGen(opts.output, fds, opts.pluginDefs)
	}
	if err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func usage(exitCode int) {
	// TODO
	os.Exit(exitCode)
}

func loadDescriptors(fileNames []string) ([]*desc.FileDescriptor, error) {
	// TODO
	return nil,  nil
}