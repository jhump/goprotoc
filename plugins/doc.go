// Package plugins contains functions that protoc plugins can use to
// simplify the task of implementing the plugin interface and generating
// Go code.
//
// # Interface for Protoc Plugins
//
// A protoc plugin need only provide a function whose signature matches the
// Plugin type and then wire it up in a main method like so:
//
//	func main() {
//	    plugins.PluginMain(doCodeGen)
//	}
//
//	func doCodeGen(req  *plugins.CodeGenRequest,
//	               resp *plugins.CodeGenResponse) error {
//	    // ...
//	    // Process req, generate code to resp
//	    // ...
//	}
//
// # Code Generation Helpers
//
// This package has numerous helpful types for generating Go code. For
// example, GoNames provides numerous functions for computing the names
// and types of elements generated by the standard protoc-gen-go plugin.
// This makes it easy to generate code that references these types
// and/or augments these types.
package plugins
