package plugins

import (
	"fmt"
	"sync"
)

var (
	pluginReg   = map[string]Plugin{}
	pluginRegMu sync.Mutex
)

// RegisterPlugin registers a plugin with the given name. Programs compiled as a
// Go plugin should call this method in an init() function so that, when loaded,
// their protoc plugin logic can be included in a protoc invocation of the
// protoc-gen-gox plugin.
func RegisterPlugin(name string, plugin Plugin) {
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	if _, ok := pluginReg[name]; ok {
		panic(fmt.Sprintf("plugin with name %s already registered", name))
	}
	pluginReg[name] = plugin
}

// GetRegisteredPlugins gets a map of all registered plugins, keyed by name.
func GetRegisteredPlugins() map[string]Plugin {
	ret := map[string]Plugin{}
	pluginRegMu.Lock()
	defer pluginRegMu.Unlock()
	for k, v := range pluginReg {
		ret[k] = v
	}
	return ret
}
