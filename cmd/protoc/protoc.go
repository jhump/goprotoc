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
	"io/ioutil"
	"os"
	"strings"

	"github.com/jhump/protoreflect/desc/protoparse"

	"github.com/jhump/goprotoc/plugins"
)

const protocVersionEmu = "goprotoc 3.5.1"
var gitSha = "" // can be replaced by -X linker flag

var (
	// flags and args
	includePaths          []string
	encodeType            string
	decodeType            string
	decodeRaw             bool
	inputDescriptors      []string
	outputDescriptor      string
	includeImports        bool
	includeSourceInfo     bool
	printFreeFieldNumbers bool
	pluginDefs            []string
	output                map[string]string
	protoFiles            []string

	protocOutputs = map[string]struct{}{
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
)

func main() {
	parseFlags("", os.Args[1:], map[string]struct{}{})

	p := protoparse.Parser{
		ImportPaths:           includePaths,
		IncludeSourceCodeInfo: includeSourceInfo,
	}
	fds, err := p.ParseFiles(protoFiles...)
	if err != nil {
		fail(err.Error())
	}
}

func parseFlags(source string, args []string, sourcesSeen map[string]struct{}) {
	if _, ok := sourcesSeen[source]; ok {
		fail(fmt.Sprintf("cycle detected in option files: %s references itself (possibly indirectly)", source))
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
			protoFiles = append(protoFiles, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			protoFiles = append(protoFiles, a)
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
			fail(fmt.Sprintf("%soption %s requires an argument", loc(), parts[0]))
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
				fail(fmt.Sprintf("%soption %s does not accept an argument", loc(), parts[0]))
			}
		}

		switch parts[0] {
		case "-I", "--proto_path":
			includePaths = append(includePaths, getOptionArg())
		case "--version":
			noOptionArg()
			fmt.Printf("%s %s\n", protocVersionEmu, gitSha)
			os.Exit(0)
		case "-h", "--help":
			noOptionArg()
			usage(0)
		case "--encode":
			encodeType = getOptionArg()
		case "--decode":
			decodeType = getOptionArg()
		case "--decode_raw":
			decodeRaw = getBoolArg()
		case "--descriptor_set_in":
			inputDescriptors = append(inputDescriptors, getOptionArg())
		case "-o", "--descriptor_set_out":
			outputDescriptor = getOptionArg()
		case "--include_imports":
			includeImports = getBoolArg()
		case "--include_source_info":
			includeSourceInfo = getBoolArg()
		case "--print_free_field_numbers":
			printFreeFieldNumbers = getBoolArg()
		case "--plugin":
			pluginDefs = append(pluginDefs, getOptionArg())
		default:
			switch {
			case strings.HasPrefix(a, "@"):
				noOptionArg()
				source := a[1:]
				if contents, err := ioutil.ReadFile(source); err != nil {
					fail(fmt.Sprintf("%scould not load option file %s: %v", loc(), source, err))
				} else {
					lines := strings.Split(string(contents), "\n")
					for i := range lines {
						lines[i] = strings.TrimSpace(lines[i])
					}
					parseFlags(a[1:], lines, sourcesSeen)
				}
			case strings.HasPrefix(a, "--") && strings.HasSuffix(a, "_out"):
				output[a[2:len(a)-4]] = getOptionArg()
			default:
				fail(fmt.Sprintf("%sunrecognized option: %s", loc(), parts[0]))
			}
		}
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func usage(exitCode int) {
	// TODO
	os.Exit(exitCode)
}

func driveProtocAsPlugin(req *plugins.CodeGenRequest, resp *plugins.CodeGenResponse) error {

}