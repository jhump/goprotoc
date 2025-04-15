package goprotoc

import (
	"fmt"
	"io"
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

func parseFlags(source string, programName string, args []string, stdout io.Writer, opts *protocOptions, sourcesSeen map[string]struct{}) error {
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

		getOptionArg := func() (string, error) {
			if len(parts) > 1 {
				return parts[1], nil
			}
			if len(args) > i+1 {
				i++
				return args[i], nil
			}
			return "", fmt.Errorf("%sMissing value for flag: %s", loc(), parts[0])
		}
		getBoolArg := func() (bool, error) {
			if len(parts) > 1 {
				val := strings.ToLower(parts[1])
				switch val {
				case "true":
					return true, nil
				case "false":
					return false, nil
				default:
					return false, fmt.Errorf("%svalue for option %s must be 'true' or 'false'", loc(), parts[0])
				}
			}
			return true, nil
		}
		noOptionArg := func() error {
			if len(parts) > 1 {
				return fmt.Errorf("%s%s does not take a parameter", loc(), parts[0])
			}
			return nil
		}

		switch parts[0] {
		case "-I", "--proto_path":
			value, err := getOptionArg()
			if err != nil {
				return err
			}
			opts.includePaths = append(opts.includePaths, value)
		case "--version":
			if err := noOptionArg(); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(stdout, "goprotoc %s (proto %s)\n", version, protocVersionEmu); err != nil {
				return err
			}
			return errVersion
		case "-h", "--help":
			if err := noOptionArg(); err != nil {
				return err
			}
			if err := usage(programName, stdout); err != nil {
				return err
			}
			return errUsage
		case "--encode":
			value, err := getOptionArg()
			if err != nil {
				return err
			}
			opts.encodeType = value
		case "--decode":
			value, err := getOptionArg()
			if err != nil {
				return err
			}
			opts.decodeType = value
		case "--decode_raw":
			value, err := getBoolArg()
			if err != nil {
				return err
			}
			opts.decodeRaw = value
		case "--descriptor_set_in":
			value, err := getOptionArg()
			if err != nil {
				return err
			}
			opts.inputDescriptors = append(opts.inputDescriptors, value)
		case "-o", "--descriptor_set_out":
			value, err := getOptionArg()
			if err != nil {
				return err
			}
			opts.outputDescriptor = value
		case "--include_imports":
			value, err := getBoolArg()
			if err != nil {
				return err
			}
			opts.includeImports = value
		case "--include_source_info":
			value, err := getBoolArg()
			if err != nil {
				return err
			}
			opts.includeSourceInfo = value
		case "--print_free_field_numbers":
			value, err := getBoolArg()
			if err != nil {
				return err
			}
			opts.printFreeFieldNumbers = value
		case "--plugin":
			value, err := getOptionArg()
			if err != nil {
				return err
			}
			plDef := strings.SplitN(value, "=", 2)
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
			if opts.pluginDefs == nil {
				opts.pluginDefs = make(map[string]string, 1)
			}
			opts.pluginDefs[pluginName] = pluginLocation
		default:
			switch {
			case strings.HasPrefix(a, "@"):
				if err := noOptionArg(); err != nil {
					return err
				}
				source := a[1:]
				contents, err := os.ReadFile(source)
				if err != nil {
					return fmt.Errorf("%scould not load option file %s: %v", loc(), source, err)
				}
				lines := strings.Split(string(contents), "\n")
				for i := range lines {
					lines[i] = strings.TrimSpace(lines[i])
				}
				if err := parseFlags(a[1:], programName, lines, stdout, opts, sourcesSeen); err != nil {
					return err
				}
			case strings.HasPrefix(parts[0], "--") && strings.HasSuffix(parts[0], "_out"):
				value, err := getOptionArg()
				if err != nil {
					return err
				}
				if opts.output == nil {
					opts.output = make(map[string]string, 1)
				}
				opts.output[parts[0][2:len(parts[0])-4]] = value
			default:
				return fmt.Errorf("%sunrecognized option: %s", loc(), parts[0])
			}
		}
	}
	return nil
}
