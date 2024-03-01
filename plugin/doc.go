// Package plugin contains functions that protoc plugins can use to
// simplify the task of implementing the plugin interface and generating
// code.
//
// # Interface for Protoc Plugins
//
// A protoc plugin need only provide a function whose signature matches the
// Plugin type and then wire it up in a main method like so:
//
//	func main() {
//	    plugin.Main(doCodeGen)
//	}
//
//	func doCodeGen(req  *plugin.CodeGenRequest,
//	               resp *plugin.CodeGenResponse) error {
//	    // ...
//	    // Process req, generate code to resp
//	    // ...
//	}
package plugin
