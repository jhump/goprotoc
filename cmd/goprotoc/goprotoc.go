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
	fmt.Printf(
		`Usage: %s [OPTION] PROTO_FILES
Parse PROTO_FILES and generate output based on the options given:
  -IPATH, --proto_path=PATH   Specify the directory in which to search for
                              imports.  May be specified multiple times;
                              directories will be searched in order.  If not
                              given, the current working directory is used.
  --version                   Show version info and exit.
  -h, --help                  Show this text and exit.
  --encode=MESSAGE_TYPE       Read a text-format message of the given type
                              from standard input and write it in binary
                              to standard output.  The message type must
                              be defined in PROTO_FILES or their imports.
  --decode=MESSAGE_TYPE       Read a binary message of the given type from
                              standard input and write it in text format
                              to standard output.  The message type must
                              be defined in PROTO_FILES or their imports.
  --decode_raw                Read an arbitrary protocol message from
                              standard input and write the raw tag/value
                              pairs in text format to standard output.  No
                              PROTO_FILES should be given when using this
                              flag.
  --descriptor_set_in=FILES   Specifies a delimited list of FILES
                              each containing a FileDescriptorSet (a
                              protocol buffer defined in descriptor.proto).
                              The FileDescriptor for each of the PROTO_FILES
                              provided will be loaded from these
                              FileDescriptorSets. If a FileDescriptor
                              appears multiple times, the first occurrence
                              will be used.
  -oFILE,                     Writes a FileDescriptorSet (a protocol buffer,
    --descriptor_set_out=FILE defined in descriptor.proto) containing all of
                              the input files to FILE.
  --include_imports           When using --descriptor_set_out, also include
                              all dependencies of the input files in the
                              set, so that the set is self-contained.
  --include_source_info       When using --descriptor_set_out, do not strip
                              SourceCodeInfo from the FileDescriptorProto.
                              This results in vastly larger descriptors that
                              include information about the original
                              location of each decl in the source file as
                              well as surrounding comments.
  --print_free_field_numbers  Print the free field numbers of the messages
                              defined in the given proto files. Groups share
                              the same field number space with the parent
                              message. Extension ranges are counted as
                              occupied fields numbers.
  --plugin=EXECUTABLE         Specifies a plugin executable to use.
                              Normally, protoc searches the PATH for
                              plugins, but you may specify additional
                              executables not in the path using this flag.
                              Additionally, EXECUTABLE may be of the form
                              NAME=PATH, in which case the given plugin name
                              is mapped to the given executable even if
                              the executable's own name differs.
  --<PLUGIN>_out=OUT_DIR      Invokes the plugin named <PLUGIN>, instructing
                              it to generate source code into the given
                              OUT_DIR. The given OUT_DIR can be in the
                              extended form ARGS:OUT_DIR, in which case ARGS
                              are extra arguments/flags to pass to the
                              plugin.
                              The plugin binary is located by searching for
                              for any plugin locations configured with
                              --plugin flags. If no such flags were provided
                              for the named plugin, then an executable named
                              'protoc-gen-<PLUGIN>' is used.
                              If the named plugin is 'cpp', 'csharp', 'java',
                              'javanano', 'js', 'objc', 'php', 'python', or
                              'ruby' then the protoc binary is used to
                              generate the output code (instead of some
                              plugin).
  @<filename>                 Read options and filenames from file. If a
                              relative file path is specified, the file
                              will be searched in the working directory.
                              The --proto_path option will not affect how
                              this argument file is searched. Content of
                              the file will be expanded in the position of
                              @<filename> as in the argument list. Note
                              that shell expansion is not applied to the
                              content of the file (i.e., you cannot use
                              quotes, wildcards, escapes, commands, etc.).
                              Each line corresponds to a single argument,
                              even if it contains spaces.
`, os.Args[0])
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
