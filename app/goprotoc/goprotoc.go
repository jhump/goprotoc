// Package goprotoc implements the goprotoc command logic.
package goprotoc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

//lint:file-ignore ST1005 capitalized errors that are sentences are command return values printed to stderr

const protocVersionEmu = "3.5.1"

var (
	version    = "dev build <no version set>" // can be replaced by -X linker flag
	errVersion = errors.New("__version_printed__")
	errUsage   = errors.New("__usage_printed__")
)

// Main is the entrypoint for the program.
func Main() {
	os.Exit(Run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}

// Run runs the program and returns the exit code.
func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if err := run(args, stdin, stdout, stderr); err != nil {
		message := err.Error()
		if message == "" {
			message = "unexpected error"
		}
		_, _ = fmt.Fprintln(stderr, message)
		return 1
	}
	return 0
}

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	var opts protocOptions
	if err := parseFlags("", args[0], args[1:], stdout, &opts, map[string]struct{}{}); err != nil {
		switch err {
		case errVersion, errUsage:
			return nil
		default:
			return err
		}
	}

	if len(opts.inputDescriptors) > 0 && len(opts.includePaths) > 0 {
		return errors.New("Only one of --descriptor_set_in and --proto_path can be specified.")
	}

	if len(opts.protoFiles) == 0 && !opts.decodeRaw {
		return errors.New("Missing input file.")
	} else if len(opts.protoFiles) > 0 && opts.decodeRaw {
		return errors.New("When using --decode_raw, no input files should be given.")
	}

	var fds []*desc.FileDescriptor
	if len(opts.protoFiles) > 0 {
		if len(opts.inputDescriptors) > 0 {
			var err error
			if fds, err = loadDescriptors(opts.inputDescriptors, opts.protoFiles); err != nil {
				return err
			}
		} else {
			includeSourceInfo := opts.includeSourceInfo
			// We have to pass SourceCodeInfo to plugins as they expect this information to generate comments.
			// This is true for the builtin protoc plugins as well.
			// We could instead do a separate Parse if we wanted but the logic gets very complicated
			// As we would want to make sure we are ONLY outputting to plugins and nothing else
			// So that we don't have to parse twice in the general case.
			if len(opts.output) > 0 {
				includeSourceInfo = true
			}
			var err error
			if opts.protoFiles, err = protoparse.ResolveFilenames(opts.includePaths, opts.protoFiles...); err != nil {
				return err
			}
			var errs []error
			p := protoparse.Parser{
				ImportPaths:           opts.includePaths,
				IncludeSourceCodeInfo: includeSourceInfo,
				ErrorReporter: func(err protoparse.ErrorWithPos) error {
					if len(errs) >= 20 {
						return errors.New("Too many errors... aborting.")
					}
					errs = append(errs, err)
					return nil
				},
			}
			if fds, err = p.ParseFiles(opts.protoFiles...); err != nil && err != protoparse.ErrInvalidSource {
				errs = append(errs, err)
			}
			err = toError(errs)
			if err != nil {
				return err
			}
		}
	}

	doingCodeGen := len(opts.output) > 0 || opts.outputDescriptor != ""
	if doingCodeGen && opts.encodeType != "" {
		return errors.New("Cannot use --encode and generate code or descriptors at the same time.")
	}
	if doingCodeGen && (opts.decodeType != "" || opts.decodeRaw) {
		return errors.New("Cannot use --decode and generate code or descriptors at the same time.")
	}
	if opts.encodeType != "" && (opts.decodeType != "" || opts.decodeRaw) {
		return errors.New("Only one of --encode and --decode can be specified.")
	}

	var err error
	switch {
	case opts.encodeType != "":
		err = doEncode(opts.encodeType, fds, stdin, stdout)
	case opts.decodeType != "":
		err = doDecode(opts.decodeType, fds, stdin, stdout)
	case opts.decodeRaw:
		err = doDecodeRaw(stdin, stdout)
	case opts.printFreeFieldNumbers:
		err = doPrintFreeFieldNumbers(fds, stdout)
	default:
		if !doingCodeGen {
			return errors.New("Missing output directives.")
		}
		if len(opts.output) > 0 {
			err = doCodeGen(opts.output, fds, opts.pluginDefs)
		}
		if err == nil && opts.outputDescriptor != "" {
			err = saveDescriptor(opts.outputDescriptor, fds, opts.includeImports, opts.includeSourceInfo)
		}
	}
	return err
}

func usage(programName string, stdout io.Writer) error {
	_, err := fmt.Fprintf(
		stdout,
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
`, programName)
	return err
}

func loadDescriptors(descFileNames []string, inputProtoFiles []string) ([]*desc.FileDescriptor, error) {
	allFiles := map[string]*descriptorpb.FileDescriptorProto{}
	for _, fileName := range descFileNames {
		d, err := os.ReadFile(fileName)
		if err != nil {
			return nil, err
		}
		var set descriptorpb.FileDescriptorSet
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

func linkFile(fileName string, fds map[string]*descriptorpb.FileDescriptorProto, linkedFds map[string]*desc.FileDescriptor, seen []string) (*desc.FileDescriptor, error) {
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
	var fileNames map[string]struct{}
	if !includeImports {
		// if we aren't including imports, then we need a set of file names that
		// are included so we can create a topologically sorted list w/out
		// including imports that should not be present.
		fileNames = map[string]struct{}{}
		for _, fd := range fds {
			fileNames[fd.GetName()] = struct{}{}
		}
	}

	var fdSet descriptorpb.FileDescriptorSet
	alreadyExported := map[string]struct{}{}
	for _, fd := range fds {
		toFileDescriptorSet(alreadyExported, fileNames, &fdSet, fd, includeImports, includeSourceInfo)
	}
	b, err := proto.Marshal(&fdSet)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0666)
}

func toFileDescriptorSet(alreadySeen, fileNames map[string]struct{}, fdSet *descriptorpb.FileDescriptorSet, fd *desc.FileDescriptor, includeImports, includeSourceInfo bool) {
	if _, ok := alreadySeen[fd.GetName()]; ok {
		// already done this one
		return
	}
	alreadySeen[fd.GetName()] = struct{}{}

	for _, dep := range fd.GetDependencies() {
		if !includeImports {
			// we only include deps that were explicitly in the set of file names given
			if _, ok := fileNames[dep.GetName()]; !ok {
				continue
			}
		}
		toFileDescriptorSet(alreadySeen, fileNames, fdSet, dep, includeImports, includeSourceInfo)
	}

	fdp := fd.AsFileDescriptorProto()
	if !includeSourceInfo {
		// NB: this is destructive, so we need to do this step (part of saving
		// to output descriptor set file) last, *after* descriptors (and any
		// source code info) have already been used to do code gen
		fdp.SourceCodeInfo = nil
	}

	fdSet.File = append(fdSet.File, fdp)
}

func toError(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return multiError(errs)
}

type multiError []error

func (m multiError) Error() string {
	var buf bytes.Buffer
	for i, err := range m {
		if i > 0 {
			buf.WriteRune('\n')
		}
		buf.WriteString(err.Error())
	}
	return buf.String()
}
