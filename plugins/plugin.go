package plugins

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// Plugin is a code generator that generates code during protoc invocations.
// Multiple plugins can be run during the same protoc invocation.
type Plugin func(*CodeGenRequest, *CodeGenResponse) error

// CodeGenRequest represents the arguments to protoc that describe what code
// protoc has been requested to generate.
type CodeGenRequest struct {
	// Args are the parameters for the plugin.
	Args []string
	// Files are the proto source files for which code should be generated.
	Files []protoreflect.FileDescriptor
	// SourceFiles are raw descriptor protos that contain source-only options.
	// Each element corresponds to an element in Files (so the first entry in
	// this slice corresponds to the first entry in Files, etc).
	//
	// If a plugin intends to embed descriptor data in its output, it should
	// NOT use these descriptors but should instead use the descriptors in
	// RawFiles. These descriptors contain option data that is not meant to
	// be retained beyond the input source code.
	SourceFiles []*descriptorpb.FileDescriptorProto
	// RawFiles are the raw descriptor protos that back Files. This contains
	// an entry for every element in Files as well as for all dependencies.
	// These provide direct access to the underlying protos and accessing
	// them via this field is much more efficient than generating them using
	// the google.golang.org/reflect/protodesc package.
	RawFiles map[string]*descriptorpb.FileDescriptorProto
	// The version of protoc that has invoked the plugin.
	ProtocVersion *ProtocVersion
}

func (req *CodeGenRequest) toPbRequest() *pluginpb.CodeGeneratorRequest {
	var reqpb pluginpb.CodeGeneratorRequest
	if req.ProtocVersion != nil {
		reqpb.CompilerVersion = &pluginpb.Version{
			Major: proto.Int32(int32(req.ProtocVersion.Major)),
			Minor: proto.Int32(int32(req.ProtocVersion.Minor)),
			Patch: proto.Int32(int32(req.ProtocVersion.Patch)),
		}
		if req.ProtocVersion.Suffix != "" {
			reqpb.CompilerVersion.Suffix = proto.String(req.ProtocVersion.Suffix)
		}
	}

	if len(req.Args) > 0 {
		reqpb.Parameter = proto.String(strings.Join(req.Args, ","))
	}

	reqpb.FileToGenerate = make([]string, len(req.Files))
	for i, fd := range req.Files {
		reqpb.FileToGenerate[i] = fd.Path()
	}
	var files []*descriptorpb.FileDescriptorProto
	for _, fd := range req.Files {
		req.addRecursive(fd, &files, map[string]struct{}{})
	}
	reqpb.ProtoFile = files
	reqpb.SourceFileDescriptors = req.SourceFiles

	return &reqpb
}

func (req *CodeGenRequest) addRecursive(fd protoreflect.FileDescriptor, files *[]*descriptorpb.FileDescriptorProto, seen map[string]struct{}) {
	if _, ok := seen[fd.Path()]; ok {
		return
	}
	seen[fd.Path()] = struct{}{}

	deps := fd.Imports()
	for i, length := 0, deps.Len(); i < length; i++ {
		req.addRecursive(deps.Get(i).FileDescriptor, files, seen)
		*files = append(*files, req.RawFiles[fd.Path()])
	}
}

// CodeGenResponse is how the plugin transmits generated code to protoc.
type CodeGenResponse struct {
	pluginName string
	output     *outputMap
	features   uint64
}

type outputMap struct {
	mu    sync.Mutex
	files map[result][]data
}

type result struct {
	name, insertionPoint string
}

type data struct {
	plugin   string
	contents io.Reader
}

func (m *outputMap) addSnippet(pluginName, name, insertionPoint string, contents io.Reader) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := result{name: name, insertionPoint: insertionPoint}
	if m.files == nil {
		m.files = map[result][]data{}
	}
	if insertionPoint == "" {
		// can only create one file per name, but can create multiple snippets
		// that will be concatenated together
		if d := m.files[key]; len(d) > 0 {
			panic(fmt.Sprintf("file %s already opened for writing by plugin %s", name, d[0].plugin))
		}
	}
	m.files[key] = append(m.files[key], data{plugin: pluginName, contents: contents})
}

// OutputSnippet returns a writer for creating the snippet to be stored in the
// given file name at the given insertion point. Insertion points are generally
// not used when producing Go code since Go allows multiple files in the same
// package to all contribute to the package's contents. But insertion points can
// be valuable for other languages where certain kinds of language elements must
// appear in particular files or in particular locations within a file.
func (resp *CodeGenResponse) OutputSnippet(name, insertionPoint string) io.Writer {
	var buf bytes.Buffer
	resp.output.addSnippet(resp.pluginName, name, insertionPoint, &buf)
	return &buf
}

// OutputFile returns a writer for creating the file with the given name.
func (resp *CodeGenResponse) OutputFile(name string) io.Writer {
	return resp.OutputSnippet(name, "")
}

// ForEach invokes the given function for each output in the response so far.
// The given reader provides access to examine the file/snippet contents. If the
// function returns an error, ForEach stops iteration and returns that error.
func (resp *CodeGenResponse) ForEach(fn func(name, insertionPoint string, data io.Reader) error) error {
	resp.output.mu.Lock()
	defer resp.output.mu.Unlock()
	for res, ds := range resp.output.files {
		for _, d := range ds {
			if err := fn(res.name, res.insertionPoint, d.contents); err != nil {
				return err
			}
		}
	}
	return nil
}

// SupportsFeatures allows the plugin to communicate which code generation features that
// it supports.
func (resp *CodeGenResponse) SupportsFeatures(feature ...pluginpb.CodeGeneratorResponse_Feature) {
	for _, f := range feature {
		resp.features |= uint64(f)
	}
}

// ProtocVersion represents a version of the protoc tool.
type ProtocVersion struct {
	Major, Minor, Patch int
	Suffix              string
}

func (v *ProtocVersion) String() string {
	if v == nil {
		return "(unknown)"
	}
	var buf bytes.Buffer
	_, _ = fmt.Fprintf(&buf, "%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Suffix != "" {
		if v.Suffix[0] != '-' {
			buf.WriteRune('-')
		}
		buf.WriteString(v.Suffix)
	}
	return buf.String()
}

// NewCodeGenResponse creates a new response for the named plugin. If other is
// non-nil, files added to the returned response will be contributed to other.
func NewCodeGenResponse(pluginName string, other *CodeGenResponse) *CodeGenResponse {
	var output *outputMap
	if other != nil {
		output = other.output
	} else {
		output = &outputMap{}
	}
	return &CodeGenResponse{
		pluginName: pluginName,
		output:     output,
	}
}
