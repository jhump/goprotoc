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
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"

	"github.com/jhump/goprotoc/plugins"
	"golang.org/x/net/context"
	"unicode"
)

const protocVersionEmu = "goprotoc 3.5.1"

var protocVersionStruct = plugins.ProtocVersion{
	Major:  3,
	Minor:  5,
	Patch:  1,
	Suffix: "go",
}

var gitSha = "" // can be replaced by -X linker flag

type protocOptions struct {
	includePaths          []string
	encodeType            string
	decodeType            string
	decodeRaw             bool
	inputDescriptors      []string
	outputDescriptor      string
	includeImports        bool
	includeSourceInfo     bool
	printFreeFieldNumbers bool
	pluginDefs            map[string]string
	output                map[string]string
	protoFiles            []string
}

var protocOutputs = map[string]struct{}{
	"cpp":      {},
	"csharp":   {},
	"java":     {},
	"javanano": {},
	"js":       {},
	"objc":     {},
	"php":      {},
	"python":   {},
	"ruby":     {},
}

func main() {
	opts, err := parseFlags("", os.Args[1:], map[string]struct{}{})
	if err != nil {
		fail(err.Error())
	}

	if len(opts.inputDescriptors) > 0 && len(opts.includePaths) > 0 {
		// TODO: error
		_ = 0
	}

	var fds []*desc.FileDescriptor
	if len(opts.inputDescriptors) > 0 {
		// TODO
		_ = 0
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

	if opts.encodeType != "" {
		// TODO: do encoding
		_ = 0
	}
	if opts.decodeType != "" {
		// TODO: do decoding
		_ = 0
	}
	if opts.decodeRaw {
		// TODO: do decoding
		_ = 0
	}
	if opts.printFreeFieldNumbers {
		// TODO: print field numbers
		_ = 0
	}

	// TODO: factor this into a function
	// code gen
	if len(opts.output) == 0 {
		fail("Missing output directives.")
	}
	req := plugins.CodeGenRequest{
		Files:         fds,
		ProtocVersion: protocVersionStruct,
	}
	resps := map[string]*plugins.CodeGenResponse{}
	locations := map[string]string{}
	for lang, loc := range opts.output {
		resp := plugins.NewCodeGenResponse(lang, nil)
		resps[lang] = resp
		locParts := strings.SplitN(loc, ":", 2)
		var arg string
		if len(locParts) > 1 {
			arg = locParts[0]
			locations[lang] = locParts[1]
		} else {
			locations[lang] = loc
		}
		if err := executePlugin(&req, resp, opts, lang, arg); err != nil {
			fail(err.Error())
		}
	}
	results := map[string]fileOutput{}
	for lang, resp := range resps {
		err := resp.ForEach(func(name, insertionPoint string, data io.Reader) error {
			loc := locations[lang]
			fullName, err := filepath.Abs(filepath.Join(loc, name))
			if err != nil {
				return err
			}
			o := results[fullName]
			if insertionPoint == "" {
				if o.createdBy != "" {
					// TODO: error
					_ = 0
				}
				o.contents = data
				o.createdBy = lang
			} else {
				if o.insertions == nil {
					o.insertions = map[string][]insertedContent{}
					o.insertsFrom = map[string]struct{}{}
				}
				content := insertedContent{data: data, lang: lang}
				o.insertions[insertionPoint] = append(o.insertions[insertionPoint], content)
				o.insertsFrom[lang] = struct{}{}
			}
			results[fullName] = o
			return nil
		})
		if err != nil {
			fail(err.Error())
		}
	}

	for fileName, output := range results {
		if output.contents == nil {
			// TODO: fail
			_ = 0
		}
		fileContents := output.contents
		if len(output.insertions) > 0 {
			fileContents, err = applyInsertions(output.contents, output.insertions)
			if err != nil {
				fail(err.Error())
			}
		}
		w, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
		if err != nil {
			// TODO: fail
			_ = 0
		}
		_, err = io.Copy(w, fileContents)
		if err != nil {
			// TODO: fail
			_ = 0
		}
	}
}

type fileOutput struct {
	contents    io.Reader
	createdBy   string
	insertions  map[string][]insertedContent
	insertsFrom map[string]struct{}
}

func parseFlags(source string, args []string, sourcesSeen map[string]struct{}) (*protocOptions, error) {
	var opts protocOptions
	if _, ok := sourcesSeen[source]; ok {
		return nil, fmt.Errorf("cycle detected in option files: %s references itself (possibly indirectly)", source)
	}
	sourcesSeen[source] = struct{}{}

	for i := 0; i < len(args); i++ {
		loc := func() string {
			if source == "" {
				return ""
			}
			return fmt.Sprintf("%s:%d: ", source, i+1)
		}

		a := args[i]
		if a == "--" {
			opts.protoFiles = append(opts.protoFiles, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			opts.protoFiles = append(opts.protoFiles, a)
			continue
		}
		parts := strings.SplitN(args[i], "=", 2)

		getOptionArg := func() string {
			if len(parts) > 1 {
				return parts[1]
			}
			if len(args) > i+1 {
				i++
				return args[i]
			}
			fail(fmt.Sprintf("%sMissing value for flag: %s", loc(), parts[0]))
			return "" // make compiler happy
		}
		getBoolArg := func() bool {
			if len(parts) > 1 {
				val := strings.ToLower(parts[1])
				switch val {
				case "true":
					return true
				case "false":
					return false
				default:
					fail(fmt.Sprintf("%svalue for option %s must be 'true' or 'false'", loc(), parts[0]))
				}
			}
			return true
		}
		noOptionArg := func() {
			if len(parts) > 1 {
				fail(fmt.Sprintf("%s%s does not take a parameter", loc(), parts[0]))
			}
		}

		switch parts[0] {
		case "-I", "--proto_path":
			opts.includePaths = append(opts.includePaths, getOptionArg())
		case "--version":
			noOptionArg()
			fmt.Printf("%s %s\n", protocVersionEmu, gitSha)
			os.Exit(0)
		case "-h", "--help":
			noOptionArg()
			usage(0)
		case "--encode":
			opts.encodeType = getOptionArg()
		case "--decode":
			opts.decodeType = getOptionArg()
		case "--decode_raw":
			opts.decodeRaw = getBoolArg()
		case "--descriptor_set_in":
			opts.inputDescriptors = append(opts.inputDescriptors, getOptionArg())
		case "-o", "--descriptor_set_out":
			opts.outputDescriptor = getOptionArg()
		case "--include_imports":
			opts.includeImports = getBoolArg()
		case "--include_source_info":
			opts.includeSourceInfo = getBoolArg()
		case "--print_free_field_numbers":
			opts.printFreeFieldNumbers = getBoolArg()
		case "--plugin":
			plDef := strings.SplitN(getOptionArg(), "=", 2)
			if len(plDef) == 0 {
				return nil, fmt.Errorf("--plugin argument must not be blank")
			}
			var pluginName, pluginLocation string
			if len(plDef) == 1 {
				pluginName = filepath.Base(plDef[0])
				pluginLocation = plDef[0]
			} else {
				pluginName = plDef[0]
				pluginLocation = plDef[1]
			}
			if !strings.HasPrefix(pluginName, "protoc-gen-") {
				return nil, fmt.Errorf("plugin name %s is not valid: name should have 'protoc-gen-' prefix", pluginName)
			}
			pluginName = pluginName[len("protoc-gen-"):]
			opts.pluginDefs[pluginName] = pluginLocation
		default:
			switch {
			case strings.HasPrefix(a, "@"):
				noOptionArg()
				source := a[1:]
				if contents, err := ioutil.ReadFile(source); err != nil {
					return nil, fmt.Errorf("%scould not load option file %s: %v", loc(), source, err)
				} else {
					lines := strings.Split(string(contents), "\n")
					for i := range lines {
						lines[i] = strings.TrimSpace(lines[i])
					}
					parseFlags(a[1:], lines, sourcesSeen)
				}
			case strings.HasPrefix(a, "--") && strings.HasSuffix(a, "_out"):
				opts.output[a[2:len(a)-4]] = getOptionArg()
			default:
				return nil, fmt.Errorf("%sunrecognized option: %s", loc(), parts[0])
			}
		}
	}
	return &opts, nil
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func usage(exitCode int) {
	// TODO
	os.Exit(exitCode)
}

func executePlugin(req *plugins.CodeGenRequest, resp *plugins.CodeGenResponse, opts *protocOptions, lang, outputArg string) error {
	req.Args = strings.Split(outputArg, ",")
	pluginName := opts.pluginDefs[lang]
	if pluginName == "" {
		if _, ok := protocOutputs[lang]; ok {
			return driveProtocAsPlugin(req, resp, lang)
		}
		pluginName = "protoc-gen-" + lang
	}
	return plugins.Exec(context.Background(), pluginName, req, resp)
}

func driveProtocAsPlugin(req *plugins.CodeGenRequest, resp *plugins.CodeGenResponse, lang string) (err error) {
	for _, arg := range req.Args {
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("option %q for %s output does not start with '-'", arg, lang)
		}
	}

	tmpDir, err := ioutil.TempDir("", "go-protoc")
	if err != nil {
		return err
	}
	defer func() {
		cleanupErr := os.RemoveAll(tmpDir)
		if err == nil {
			err = cleanupErr
		}
	}()

	outDir := filepath.Join(tmpDir, "output")
	if err := os.Mkdir(outDir, 0777); err != nil {
		return err
	}

	fds := desc.ToFileDescriptorSet(req.Files...)
	descFile := filepath.Join(tmpDir, "descriptors")
	if fdsBytes, err := proto.Marshal(fds); err != nil {
		return err
	} else if err := ioutil.WriteFile(descFile, fdsBytes, 0666); err != nil {
		return err
	}

	args := make([]string, 2+len(req.Files)+len(req.Args))
	args[0] = "--descriptor_set_in=" + descFile
	args[1] = "--" + lang + "_out=" + outDir
	for i, arg := range req.Args {
		args[i+2] = arg
	}
	for i, f := range req.Files {
		args[i+2+len(req.Args)] = f.GetName()
	}

	cmd := exec.Command("protoc", args...)
	var combinedOutput bytes.Buffer
	cmd.Stdout = &combinedOutput
	cmd.Stderr = &combinedOutput
	if err := cmd.Run(); err != nil {
		if err, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("protoc failed to produce output for %s: %v\n%s", lang, err, combinedOutput.String())
		}
		return err
	}

	return filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if (info.Mode() & os.ModeType) != 0 {
			// not a regular file
			return nil
		}
		relPath, err := filepath.Rel(outDir, path)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out := resp.OutputFile(relPath)
		_, err = io.Copy(out, in)
		return err
	})
}

var insertionPointMarker = []byte("@@protoc_insertion_point(")

type insertedContent struct {
	data io.Reader
	lang string
}

func applyInsertions(contents io.Reader, insertions map[string][]insertedContent) (io.Reader, error) {
	var result bytes.Buffer

	var data []byte
	type toBytes interface {
		Bytes() []byte
	}
	if b, ok := contents.(toBytes); ok {
		data = b.Bytes()
	} else {
		var err error
		data, err = ioutil.ReadAll(contents)
		if err != nil {
			return nil, err
		}
	}

	for {
		pos := bytes.Index(data, insertionPointMarker)
		if pos < 0 {
			break
		}
		startPos := pos + len(insertionPointMarker)
		endPos := bytes.IndexByte(data[startPos:], ')')
		if endPos < 0 {
			// malformed marker! skip it
			break
		}
		point := string(data[startPos:endPos])
		insertedData := insertions[point]
		if len(insertedData) == 0 {
			result.Write(data[:endPos+1])
			data = data[endPos+1:]
			continue
		}

		delete(insertions, point)

		prevLine := bytes.LastIndexByte(data[:pos], '\n')
		prevComment := bytes.LastIndexByte(data[prevLine+1:pos], '/')
		var insertionIndex int
		var sep, indent []byte
		if prevComment != -1 &&
			data[prevLine+1+prevComment+1] == '*' &&
			len(bytes.TrimSpace(data[prevLine+1+prevComment+2:pos])) == 0 {
			// insertion point preceded by "/* ", so we insert directly before
			// that with no indentation
			insertionIndex = prevLine + 1 + prevComment
			sep = []byte{' '}
		} else {
			// otherwise, insert before the insertion point line, using same
			// indent as observed on insertion point line
			insertionIndex = prevLine + 1
			sep = []byte{'\n'}
			line := data[insertionIndex:pos]
			trimmedLine := bytes.TrimLeftFunc(line, unicode.IsSpace)
			if len(line) > len(trimmedLine) {
				indent = line[:len(line)-len(trimmedLine)]
			}
		}

		result.Write(data[:insertionIndex])
		for _, ins := range insertedData {
			if len(indent) == 0 {
				if _, err := io.Copy(&result, ins.data); err != nil {
					return nil, err
				}
			} else {
				// if there's an indent, break up the inserted data
				// into lines and prefix each line with the indent
				insData, err := ioutil.ReadAll(ins.data)
				if err != nil {
					return nil, err
				}
				lines := bytes.Split(insData, []byte{'\n'})
				for _, line := range lines {
					result.Write(indent)
					result.Write(line)
				}
			}

			if !bytes.HasSuffix(result.Bytes(), sep) {
				result.Write(sep)
			}
		}
		result.Write(data[insertionIndex : endPos+1])
		data = data[endPos+1:]
	}

	if len(insertions) > 0 {
		// TODO: fail with missing insertion points
		_ = 0
	}

	result.Write(data)
	return &result, nil
}
