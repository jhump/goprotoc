// Command protoc-gen-gox is a protoc plugin. It is a dispatcher that will run
// protoc-gen-go as well as other protoc plugins that also generate Go code.
//
// # Protoc Arguments
//
// By default, it will just run protoc-gen-go, as if a --go_out parameter had
// been present on the protoc command-line. Before the output location, you can
// indicate a config file and also enable or disable particular plugins. These
// arguments are a comma-separated list, followed by a colon and then the output
// location:
//
//	protoc --gox_out=-go,+foo:./ test.proto
//
// The allowed args are:
//  1. "config=<filename>": A yaml file that contains configuration for the
//     plugins that protoc-gen-gox will run.
//  2. "plugin_path=<list>": Indicates a pipe-delimited list of directories
//     where protoc-gen-gox will search for its plugins. If a plugin is not
//     found in this plugin path, the directories in the PATH environment
//     variable will also be searched.
//  3. "+<plugin>": Indicates that the named plugin should be run, even if it
//     is not referenced in any given config file.
//  4. "-<plugin>": Indicates that the named plugin should NOT be run. Its
//     configuration in any named config file is ignored. This is the only way
//     to prevent the standard go plugin (protoc-gen-go) from running since it
//     will run under normal circumstances, even without any configuration.
//
// A plugin may be referenced via its full name, such as "protoc-gen-go", or via
// its short name, such as "go". Furthermore, the actual plugin file/executable
// is not required to have the "protoc-gen-" prefix.
//
// The plugin name "go-grpc" is a pseudo-plugin. When enabled or disabled, it
// means to add or remove the "grpc" label from any "plugins" arg for the
// standard go plugin (protoc-gen-go). You can enable or disable it from the
// protoc args using a "+go-grpc" or "-go-grpc" arg to the gox plugin. It is
// not allowed to configure this psuedo-plugin in a config file: configure the
// standard "go" plugin instead with a "plugins=grpc" argument.
//
// # Config File
//
// The config file, optionally indicated by a "config=<filename>" argument, must
// be a YAML file. Its format is as follows:
//
//	# optional list of directories to search for plugins
//	plugin_path: ["/foo", "/bar", "/baz"]
//
//	# optional list of parameters to pass to *every* plugin
//	common_params: []
//
//	# other keys indicate plugin names and their config
//	plugin_name:
//	  # optional path to where plugin file resides - can be path to
//	  # plugin itself or directory that contains plugin
//	  location: "/foo/bar/plugin_name"
//	  # optional arguments to supply to this plugin
//	  params: ["frobnitz=off"]
//
//	# other keys can use full name of plugins that follow protoc convention
//	# (but don't have to: "foobar" could also be used for this one):
//	protoc-gen-foobar: {} # empty config is fine
//
// # Go Plugins
//
// The protoc-gen-gox program can load Go plugins and execute them (instead of
// forking them as separate executables). If a given protoc plugin binary is
// compiled as a Go plugin, then it should register itself from an init function
// using the goxplugin.Register function. The protoc-gen-gox program will then
// link in the Go plugin at runtime and execute any such plugins that were
// registered when the plugin binary was initialized. If a given protoc plugin
// is *not* a Go plugin or fails to register any plugins, it will then be
// invoked as a standard protoc plugin executable.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"strings"

	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v2"

	"github.com/jhump/goprotoc/cmd/protoc-gen-gox/goxplugin"
	"github.com/jhump/goprotoc/plugins"
)

func main() {
	plugins.PluginMain(doCodeGen)
}

func doCodeGen(req *plugins.CodeGenRequest, resp *plugins.CodeGenResponse) error {
	conf, err := getConfig(req.Args)
	if err != nil {
		return err
	}
	if err := resolveLocations(conf); err != nil {
		return err
	}

	asGoPlugin := map[string]*pluginConfig{}
	asExecutable := map[string]*pluginConfig{}

	reg := goxplugin.GetAll()
	for plName, plConf := range conf.plugins {
		// try to load them as Go plugins first
		if _, err := plugin.Open(plConf.Location); err != nil {
			asExecutable[plName] = plConf
			continue
		}
		newReg := goxplugin.GetAll()
		if len(newReg) == len(reg) {
			// no new plugins registered...
			asExecutable[plName] = plConf
			continue
		}
		// whittle the list down to only newly registered plugins
		for k := range reg {
			delete(newReg, k)
		}
		// and associate config with them
		for k, v := range newReg {
			asGoPlugin[k] = plConf
			reg[k] = v
		}
	}

	// Now we can run them all in parallel.
	grp, ctx := errgroup.WithContext(context.Background())
	for plName, plConf := range asGoPlugin {
		pl := reg[plName]
		plReq := *req
		plReq.Args = plConf.Params
		plResp := plugins.NewCodeGenResponse(plName, resp)
		grp.Go(func() error {
			return pl(&plReq, plResp)
		})
	}
	for plName, plConf := range asExecutable {
		plReq := *req
		plReq.Args = plConf.Params
		plResp := plugins.NewCodeGenResponse(plName, resp)
		loc := plConf.Location
		grp.Go(func() error {
			return plugins.Exec(ctx, loc, &plReq, plResp)
		})
	}

	return grp.Wait()
}

func getConfig(args []string) (*effectiveConfig, error) {
	configFile := ""
	var pluginPath []string
	grpcEnabled := 0
	enabledPlugins := map[string]struct{}{}
	disabledPlugins := map[string]struct{}{}
	for _, a := range args {
		arg := strings.SplitN(a, "=", 2)
		switch arg[0] {
		case "plugin_path":
			if len(arg) == 1 {
				return nil, fmt.Errorf("parameter plugin_path requires a value")
			}
			pluginPath = append(pluginPath, strings.Split(arg[1], "|")...)
		case "config":
			if len(arg) == 1 {
				return nil, fmt.Errorf("parameter config requires a value")
			}
			configFile = arg[1]
		default:
			if len(arg) > 1 {
				return nil, fmt.Errorf("unrecognized parameter: %s", arg[0])
			}
			if arg[0][0] == '-' {
				name := arg[0][1:]
				if name == "go-grpc" {
					if grpcEnabled == 1 {
						return nil, fmt.Errorf("plugin grpc is both enabled and disabled")
					}
					grpcEnabled = -1
				} else {
					disabledPlugins[pluginName(name)] = struct{}{}
				}
			} else if arg[0][0] == '+' {
				name := arg[0][1:]
				if name == "go-grpc" {
					if grpcEnabled == -1 {
						return nil, fmt.Errorf("plugin grpc is both enabled and disabled")
					}
					grpcEnabled = 1
				} else {
					enabledPlugins[pluginName(name)] = struct{}{}
				}
			} else {
				return nil, fmt.Errorf("unrecognized parameter: %s", arg[0])
			}
		}
	}

	for plName := range disabledPlugins {
		if _, ok := enabledPlugins[plName]; ok {
			return nil, fmt.Errorf("plugin %s is both enabled and disabled", plName)
		}
	}

	// the standard go plugin (protoc-gen-go) is treated special since it is the
	// default that is always run unless explicitly disabled in plugin args
	goPluginEnabled := true
	if _, ok := disabledPlugins["go"]; ok {
		goPluginEnabled = false
		if grpcEnabled == 1 {
			return nil, fmt.Errorf("plugin grpc cannot be enabled when standard go plugin is disabled")
		}
	}

	var conf goxConfig
	if configFile != "" {
		b, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load config %s: %v", configFile, err)
		}
		if err := yaml.Unmarshal(b, &conf); err != nil {
			return nil, fmt.Errorf("failed to load config %s: %v", configFile, err)
		}
	}

	result := effectiveConfig{
		pluginPath: append(pluginPath, conf.PluginPath...),
		plugins:    map[string]*pluginConfig{},
	}

	for plName, plConf := range conf.Plugins {
		if plName == "go-grpc" {
			return nil, fmt.Errorf("%s: cannot configure go-grpc plugin: configure go plugin with 'plugins=grpc' parameter instead", configFile)
		}
		plName = pluginName(plName)
		if _, ok := disabledPlugins[plName]; ok {
			// disabled: skip it
			continue
		}
		if existing := result.plugins[plName]; existing != nil {
			if existing.Location != plConf.Location {
				return nil, fmt.Errorf("%s: plugin %s is configured more than once with conflicting plugin locations: %s and %s", configFile, plName, existing.Location, plConf.Location)
			}
		}
		plConf.Params = append(conf.CommonParams, plConf.Params...)
		result.plugins[plName] = plConf
	}
	for plName := range enabledPlugins {
		if _, ok := result.plugins[plName]; ok {
			// already configured
			continue
		}
		result.plugins[plName] = &pluginConfig{Params: conf.CommonParams}
	}

	if plConf, ok := result.plugins["go"]; ok {
		if grpcEnabled == -1 {
			// grpc explicitly disabled: remove it from any 'plugins' args
			params := make([]string, 0, len(plConf.Params))
			for _, p := range plConf.Params {
				if strings.HasPrefix(p, "plugins=") {
					pls := strings.Split(p[len("plugins="):], "+")
					filteredPls := make([]string, 0, len(pls))
					for _, pl := range pls {
						if pl != "grpc" {
							filteredPls = append(filteredPls, pl)
						}
					}
					if len(filteredPls) == len(pls) {
						// no change
						params = append(params, p)
					} else if len(filteredPls) > 0 {
						// grpc removed, add remaining plugins
						params = append(params, "plugins="+strings.Join(filteredPls, "+"))
					}
				} else {
					params = append(params, p)
				}
			}
			plConf.Params = params
		} else if grpcEnabled == 1 {
			// grpc explicitly enabled: make sure it is present
			plArgIndex := -1
			for i, p := range plConf.Params {
				if strings.HasPrefix(p, "plugins=") {
					plArgIndex = i
				}
			}
			if plArgIndex == -1 {
				plConf.Params = append(plConf.Params, "plugins=grpc")
			} else {
				plArg := plConf.Params[plArgIndex]
				pls := strings.Split(plArg[len("plugins="):], "+")
				found := false
				for _, pl := range pls {
					if pl == "grpc" {
						found = true
						break
					}
				}
				if !found {
					pls = append(pls, "grpc")
					plArg = "plugins=" + strings.Join(pls, "+")
					plConf.Params[plArgIndex] = plArg
				}
			}
		}
	} else if goPluginEnabled {
		// standard go plugin is enabled but no config present
		// so create a config for it
		plConf := &pluginConfig{}
		if grpcEnabled == 1 {
			plConf.Params = []string{"plugins=grpc"}
		}
		result.plugins["go"] = plConf
	}

	return &result, nil
}

type effectiveConfig struct {
	pluginPath []string
	plugins    map[string]*pluginConfig
}

type goxConfig struct {
	PluginPath   []string                 `yaml:"plugin_path,omitempty"`
	CommonParams []string                 `yaml:"common_params,omitempty"`
	Plugins      map[string]*pluginConfig `yaml:",inline"`
}

type pluginConfig struct {
	Location string   `yaml:"location,omitempty"`
	Params   []string `yaml:"params,omitempty"`
}

func resolveLocations(conf *effectiveConfig) error {
	for plName, plConf := range conf.plugins {
		paths := conf.pluginPath
		if plConf.Location != "" {
			// validate configuration if it's present
			if stat, err := os.Stat(plConf.Location); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("%s: configured location does not exist: %s", plName, plConf.Location)
				}
				return fmt.Errorf("%s: failed to stat location: %v", plName, err)
			} else if stat.IsDir() {
				loc, err := findInPath(plName, []string{plConf.Location}, false)
				if err != nil {
					return fmt.Errorf("%s: failed to stat location: %v", plName, err)
				} else if loc == "" {
					return fmt.Errorf("%s: configured location does not exist: %s", plName, plConf.Location)
				}
				plConf.Location = loc
			}
			continue
		}

		// no configured location? search the plugin path
		loc, _ := findInPath(plName, paths, true)
		if loc == "" {
			return fmt.Errorf("%s: could not find plugin in configured plugin path: %v", plName, paths)
		}
		plConf.Location = loc
	}

	return nil
}

func findInPath(name string, pluginPath []string, searchPathEnv bool) (string, error) {
	if searchPathEnv {
		if pathEnv := os.Getenv("PATH"); pathEnv != "" {
			paths := strings.Split(pathEnv, string(filepath.ListSeparator))
			pluginPath = append(pluginPath, paths...)
		}
	}

	var lastErr error

	for _, path := range pluginPath {
		for _, prefix := range []string{"", "protoc-gen-"} {
			loc := filepath.Join(path, prefix+name)
			if _, err := os.Stat(loc); err == nil {
				return loc, nil
			} else if !os.IsNotExist(err) {
				lastErr = err
			}
		}
	}

	return "", lastErr
}

func pluginName(name string) string {
	if strings.HasPrefix(name, "protoc-gen-") {
		return name[len("protoc-gen-"):]
	}
	return name
}
