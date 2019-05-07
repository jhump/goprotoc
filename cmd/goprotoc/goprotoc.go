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
// driving it as if it were a plugin. In this mode, it provides to protoc
// the file descriptors it has already parsed, instead of asking protoc to
// re-parse all of the source code.
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
)

const protocVersionEmu = "goprotoc 3.5.1"

var gitSha = "" // can be replaced by -X linker flag

func main() {
	var opts protocOptions
	if err := parseFlags("", os.Args[1:], &opts, map[string]struct{}{}); err != nil {
		fail(err.Error())
	}

	if len(opts.inputDescriptors) > 0 && len(opts.includePaths) > 0 {
		fail("Only one of --descriptor_set_in and --proto_path can be specified.")
	}

	if len(opts.protoFiles) == 0 && !opts.decodeRaw {
		fail("Missing input file.")
	} else if len(opts.protoFiles) > 0 && opts.decodeRaw {
		fail("When using --decode_raw, no input files should be given.")
	}

	var fds []*desc.FileDescriptor
	if len(opts.protoFiles) > 0 {
		if len(opts.inputDescriptors) > 0 {
			var err error
			if fds, err = loadDescriptors(opts.inputDescriptors, opts.protoFiles); err != nil {
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
	}

	doingCodeGen := len(opts.output) > 0 || opts.outputDescriptor != ""
	if doingCodeGen && opts.encodeType != "" {
		fail("Cannot use --encode and generate code or descriptors at the same time.")
	}
	if doingCodeGen && (opts.decodeType != "" || opts.decodeRaw) {
		fail("Cannot use --decode and generate code or descriptors at the same time.")
	}
	if opts.encodeType != "" && (opts.decodeType != "" || opts.decodeRaw) {
		fail("Only one of --encode and --decode can be specified.")
	}

	var err error
	switch {
	case opts.encodeType != "":
		err = doEncode(opts.encodeType, fds, os.Stdin, os.Stdout)
	case opts.decodeType != "":
		err = doDecode(opts.decodeType, fds, os.Stdin, os.Stdout)
	case opts.decodeRaw:
		err = doDecodeRaw(os.Stdin, os.Stdout)
	case opts.printFreeFieldNumbers:
		doPrintFreeFieldNumbers(fds, os.Stdout)
	default:
		if !doingCodeGen {
			fail("Missing output directives.")
		}
		if len(opts.output) > 0 {
			err = doCodeGen(opts.output, fds, opts.pluginDefs)
		}
		if err == nil && opts.outputDescriptor != "" {
			err = saveDescriptor(opts.outputDescriptor, fds, opts.includeImports, opts.includeSourceInfo)
		}
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

func loadDescriptors(descFileNames []string, inputProtoFiles []string) ([]*desc.FileDescriptor, error) {
	allFiles := map[string]*descriptor.FileDescriptorProto{}
	for _, fileName := range descFileNames {
		d, err := ioutil.ReadFile(fileName)
		if err != nil {
			return nil, err
		}
		var set descriptor.FileDescriptorSet
		if err := proto.Unmarshal(d, &set); err != nil {
			return nil, fmt.Errorf("file %q is not a valid file descriptor set: %v", fileName, err)
		}
		for _, fd := range set.File {
			if _, ok := allFiles[fd.GetName()]; !ok {
				// only load into allFiles map if not already present: we keep
				// only the first file found for a given name
				allFiles[fd.GetName()] = fd
			}
		}
	}
	result := make([]*desc.FileDescriptor, len(inputProtoFiles))
	linked := map[string]*desc.FileDescriptor{}
	for i, protoName := range inputProtoFiles {
		if _, ok := allFiles[protoName]; !ok {
			return nil, fmt.Errorf("file not found: %q", protoName)
		}
		var err error
		result[i], err = linkFile(protoName, allFiles, linked, nil)
		if err != nil {
			return nil, fmt.Errorf("could not load %q: %v", protoName, err)
		}
	}
	return result, nil
}

func linkFile(fileName string, fds map[string]*descriptor.FileDescriptorProto, linkedFds map[string]*desc.FileDescriptor, seen []string) (*desc.FileDescriptor, error) {
	for _, name := range seen {
		if fileName == name {
			seen = append(seen, fileName)
			return nil, fmt.Errorf("cyclic imports: %v", strings.Join(seen, " -> "))
		}
	}
	seen = append(seen, fileName)
	fd, ok := linkedFds[fileName]
	if ok {
		return fd, nil
	}
	fdUnlinked, ok := fds[fileName]
	if !ok {
		return nil, fmt.Errorf("could not find dependency %q", fileName)
	}
	deps := make([]*desc.FileDescriptor, len(fdUnlinked.Dependency))
	for i, dep := range fdUnlinked.Dependency {
		var err error
		deps[i], err = linkFile(dep, fds, linkedFds, seen)
		if err != nil {
			return nil, err
		}
	}
	fd, err := desc.CreateFileDescriptor(fdUnlinked, deps...)
	if err == nil {
		linkedFds[fileName] = fd
	}
	return fd, err
}

func saveDescriptor(dest string, fds []*desc.FileDescriptor, includeImports, includeSourceInfo bool) error {
	fdsByName := map[string]*descriptor.FileDescriptorProto{}
	var fdSet descriptor.FileDescriptorSet
	for _, fd := range fds {
		toFileDescriptorSet(fdsByName, &fdSet, fd, includeImports, includeSourceInfo)
	}
	if b, err := proto.Marshal(&fdSet); err != nil {
		return err
	} else {
		return ioutil.WriteFile(dest, b, 0666)
	}
}

func toFileDescriptorSet(resultMap map[string]*descriptor.FileDescriptorProto, fdSet *descriptor.FileDescriptorSet, fd *desc.FileDescriptor, includeImports, includeSourceInfo bool) {
	if _, ok := resultMap[fd.GetName()]; ok {
		// already done this one
		return
	}

	if includeImports {
		for _, dep := range fd.GetDependencies() {
			toFileDescriptorSet(resultMap, fdSet, dep, includeImports, includeSourceInfo)
		}
	}
	fdp := fd.AsFileDescriptorProto()
	if !includeSourceInfo {
		// NB: this is destructive, so we need to do this step (part of saving
		// to output descriptor set file) last, *after* descriptors (and any
		// source code info) have already been used to do code gen
		fdp.SourceCodeInfo = nil
	}

	resultMap[fd.GetName()] = fdp
	fdSet.File = append(fdSet.File, fdp)
}