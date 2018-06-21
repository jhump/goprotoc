// Package goxplugin is for registering plugins to be executed by the
// protoc-gen-gox program. The protoc-gen-gox program will try to first load
// programs as a Go plugin. If it succeeds, any plugins that are registered (via
// the Register function in this package) will be run. If loading the program as
// a Go plugin fails (e.g. it is a normal executable, not a plugin), it will be
// executed as a normal protoc plugin.
package goxplugin

import (
	"fmt"
	"sync"

	"github.com/jhump/goprotoc/plugins"
)

var (
	pluginReg   = map[string]plugins.Plugin{}
	pluginRegMu sync.Mutex
)

// Register registers a plugin with the given name. Programs compiled as a Go
// plugin should call this method in an init() function so that, when loaded,
// their protoc plugin logic can be included in a protoc invocation of the
// protoc-gen-gox plugin.
func Register(name string, plugin plugins.Plugin) {
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	if _, ok := pluginReg[name]; ok {
		panic(fmt.Sprintf("plugin with name %s already registered", name))
	}
	pluginReg[name] = plugin
}

// GetAll gets a map of all registered plugins, keyed by name.
func GetAll() map[string]plugins.Plugin {
	ret := map[string]plugins.Plugin{}
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	for k, v := range pluginReg {
		ret[k] = v
	}
	return ret
}
