package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

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

func parseFlags(source string, args []string, opts *protocOptions, sourcesSeen map[string]struct{}) error {
	if _, ok := sourcesSeen[source]; ok {
		return fmt.Errorf("cycle detected in option files: %s references itself (possibly indirectly)", source)
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
				return fmt.Errorf("--plugin argument must not be blank")
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
				return fmt.Errorf("plugin name %s is not valid: name should have 'protoc-gen-' prefix", pluginName)
			}
			pluginName = pluginName[len("protoc-gen-"):]
			opts.pluginDefs[pluginName] = pluginLocation
		default:
			switch {
			case strings.HasPrefix(a, "@"):
				noOptionArg()
				source := a[1:]
				if contents, err := ioutil.ReadFile(source); err != nil {
					return fmt.Errorf("%scould not load option file %s: %v", loc(), source, err)
				} else {
					lines := strings.Split(string(contents), "\n")
					for i := range lines {
						lines[i] = strings.TrimSpace(lines[i])
					}
					if err := parseFlags(a[1:], lines, opts, sourcesSeen); err != nil {
						return err
					}
				}
			case strings.HasPrefix(parts[0], "--") && strings.HasSuffix(parts[0], "_out"):
				if opts.output == nil {
					opts.output = make(map[string]string, 1)
				}
				opts.output[parts[0][2:len(parts[0])-4]] = getOptionArg()
			default:
				return fmt.Errorf("%sunrecognized option: %s", loc(), parts[0])
			}
		}
	}
	return nil
}
