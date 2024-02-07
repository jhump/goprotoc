package plugins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// Exec executes the protoc plugin at the given path, sending it the given
// request and adding its generated code output to the given response.
func Exec(ctx context.Context, pluginPath string, req *CodeGenRequest, resp *CodeGenResponse) error {
	if len(req.Files) == 0 {
		return fmt.Errorf("nothing to generate: no files given")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	reqpb := req.toPbRequest()
	reqBytes, err := proto.Marshal(reqpb)
	if err != nil {
		return fmt.Errorf("failed to marshal code gen request to bytes: %v", err)
	}

	pluginName := pluginName(path.Base(pluginPath))

	cmd := exec.CommandContext(ctx, pluginPath)
	cmd.Stderr = os.Stderr
	cmd.Stdin = bytes.NewReader(reqBytes)

	respBytes, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("executing plugin %q failed: %v", pluginName, err)
	}

	var respb pluginpb.CodeGeneratorResponse
	if err := proto.Unmarshal(respBytes, &respb); err != nil {
		return fmt.Errorf("failed to unmarshal code gen response to bytes: %v", err)
	}

	if respb.Error != nil {
		return fmt.Errorf("%s", *respb.Error)
	}
	for _, res := range respb.File {
		resp.output.addSnippet(pluginName, res.GetName(), res.GetInsertionPoint(), strings.NewReader(res.GetContent()))
	}

	return nil
}

// PluginMain should be called from main functions of protoc plugins that are
// written in Go. This will handle invoking the given plugin function, handling
// any errors, writing the results to the process's stdout, and then exiting the
// process.
func PluginMain(plugin Plugin) {
	output := os.Stdout

	// We need to be strict about what goes to stdout: only the plugin response.
	// So if any code accidentally tries to print to stdout, let's have it go to
	// stderr instead.
	os.Stdout = os.Stderr

	if err := RunPlugin(os.Args[0], plugin, os.Stdin, output); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	// Success!
	os.Exit(0)
}

// RunPlugin runs the given plugin. Errors are reported using the given name.
// The protoc request is read from in and the plugin's results are written to
// out. Under most circumstances, this function will return nil, even if an
// error was encountered. That is because typically errors will be reported to
// out, by writing a code gen response that indicates the error. But if that
// fails, a non-nil error will be returned.
func RunPlugin(name string, plugin Plugin, in io.Reader, out io.Writer) error {
	name = pluginName(name)
	finish := func(respb *pluginpb.CodeGeneratorResponse) error {
		b, err := proto.Marshal(respb)
		if err != nil {
			// see if we can serialize an error response
			respb = errResponse(name, fmt.Errorf("failed to write code gen response: %v", err.Error()))
			if b, err = proto.Marshal(respb); err != nil {
				// still no? give up
				return err
			}
		}
		_, err = out.Write(b)
		return err
	}

	reqBytes, err := io.ReadAll(in)
	if err != nil {
		return finish(errResponse(name, fmt.Errorf("failed to read code gen request: %v", err)))
	}
	var reqpb pluginpb.CodeGeneratorRequest
	if err := proto.Unmarshal(reqBytes, &reqpb); err != nil {
		return finish(errResponse(name, fmt.Errorf("failed to read code gen request: %v", err)))
	}
	return finish(runPlugin(name, plugin, &reqpb))
}

func runPlugin(name string, plugin Plugin, reqpb *pluginpb.CodeGeneratorRequest) *pluginpb.CodeGeneratorResponse {
	var req CodeGenRequest

	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: reqpb.ProtoFile})
	if err != nil {
		return errResponse(name, fmt.Errorf("failed to process input descriptors: %v", err))
	}
	req.Files = make([]protoreflect.FileDescriptor, len(reqpb.FileToGenerate))
	for i, f := range reqpb.FileToGenerate {
		req.Files[i], err = files.FindFileByPath(f)
		if err != nil {
			return errResponse(name, fmt.Errorf("files to generate indicates unresolvable file %q: %v", f, err))
		}
	}
	req.SourceFiles = reqpb.SourceFileDescriptors
	req.RawFiles = make(map[string]*descriptorpb.FileDescriptorProto, len(reqpb.ProtoFile))
	for _, file := range reqpb.ProtoFile {
		req.RawFiles[file.GetName()] = file
	}
	if reqpb.Parameter != nil {
		req.Args = strings.Split(*reqpb.Parameter, ",")
	}
	if reqpb.CompilerVersion != nil {
		req.ProtocVersion.Major = int(reqpb.CompilerVersion.GetMajor())
		req.ProtocVersion.Minor = int(reqpb.CompilerVersion.GetMinor())
		req.ProtocVersion.Patch = int(reqpb.CompilerVersion.GetPatch())
		req.ProtocVersion.Suffix = reqpb.CompilerVersion.GetSuffix()
	}

	resp := NewCodeGenResponse(name, nil)

	if err := plugin(&req, resp); err != nil {
		return errResponse(name, err)
	}

	var respb pluginpb.CodeGeneratorResponse
	respb.SupportedFeatures = proto.Uint64(resp.features)
	resp.output.mu.Lock()
	defer resp.output.mu.Unlock()

	for f, d := range resp.output.files {
		genFile := pluginpb.CodeGeneratorResponse_File{
			Name: proto.String(f.name),
		}
		if f.insertionPoint != "" {
			genFile.InsertionPoint = proto.String(f.insertionPoint)
		}
		readers := make(multiReader, len(d))
		for i, r := range d {
			readers[i] = r.contents
		}
		contents, err := io.ReadAll(&readers)
		if err != nil {
			return errResponse(name, fmt.Errorf("failed to process code gen response: %v", err))
		}
		contentStr := string(contents)
		genFile.Content = &contentStr
		respb.File = append(respb.File, &genFile)
	}

	return &respb
}

func errResponse(name string, err error) *pluginpb.CodeGeneratorResponse {
	return &pluginpb.CodeGeneratorResponse{
		Error: proto.String(fmt.Sprintf("%s: %v", name, err)),
	}
}

type multiReader []io.Reader

func (r *multiReader) Read(p []byte) (int, error) {
	numRead := 0
	for {
		if len(*r) == 0 {
			return numRead, io.EOF
		}

		n, err := (*r)[0].Read(p)
		numRead += n
		if err != io.EOF {
			return numRead, err
		}

		// roll over to next reader
		p = p[n:]
		*r = (*r)[1:]
	}
}

func pluginName(name string) string {
	if strings.HasPrefix(name, "protoc-gen-") {
		return name[len("protoc-gen-"):]
	}
	return name
}
