package plugins

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// helperArg is the first argument given to the test binary when it is re-run as
// a plugin sub-process by TestExecCommand. It is not a flag, so a normal test
// run never sees it and TestHelperPlugin does nothing.
const helperArg = "__plugin_helper__"

// TestHelperPlugin is not a test. It is the plugin executed by TestExecCommand,
// which re-runs this test binary with helperArg as its first argument.
func TestHelperPlugin(t *testing.T) {
	args := flag.Args()
	if len(args) == 0 || args[0] != helperArg {
		return
	}
	// The plugin writes back the arguments it received, so that the caller can
	// verify they made it through.
	plugin := func(req *CodeGenRequest, resp *CodeGenResponse) error {
		w := resp.OutputFile("args.txt")
		_, err := fmt.Fprint(w, strings.Join(args[1:], " "))
		return err
	}
	out := os.Stdout
	os.Stdout = os.Stderr
	if err := RunPlugin("protoc-gen-helper", plugin, os.Stdin, out); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestExecCommand(t *testing.T) {
	fd, err := desc.CreateFileDescriptor(&descriptorpb.FileDescriptorProto{
		Name:   proto.String("test.proto"),
		Syntax: proto.String("proto3"),
	})
	if err != nil {
		t.Fatalf("failed to create file descriptor: %v", err)
	}

	testCases := []struct {
		name         string
		cmd          PluginCommand
		expectedArgs string
	}{
		{
			name: "explicit name and args",
			cmd: PluginCommand{
				Name: "foo",
				Path: os.Args[0],
				Args: []string{"-test.run=^TestHelperPlugin$", helperArg, "hello", "world"},
			},
			expectedArgs: "hello world",
		},
		{
			name: "name derived from path",
			cmd: PluginCommand{
				Path: os.Args[0],
				Args: []string{"-test.run=^TestHelperPlugin$", helperArg},
			},
			expectedArgs: "",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := &CodeGenRequest{Files: []*desc.FileDescriptor{fd}}
			resp := NewCodeGenResponse("test", nil)
			if err := ExecCommand(context.Background(), testCase.cmd, req, resp); err != nil {
				t.Fatalf("ExecCommand failed: %v", err)
			}

			var count int
			err := resp.ForEach(func(name, insertionPoint string, data io.Reader) error {
				count++
				if name != "args.txt" {
					t.Errorf("expected output file %q, got %q", "args.txt", name)
				}
				contents, err := io.ReadAll(data)
				if err != nil {
					return err
				}
				if string(contents) != testCase.expectedArgs {
					t.Errorf("expected args %q, got %q", testCase.expectedArgs, string(contents))
				}
				return nil
			})
			if err != nil {
				t.Fatalf("failed to read response: %v", err)
			}
			if count != 1 {
				t.Fatalf("expected 1 output file, got %d", count)
			}
		})
	}
}

func TestExecCommandNoFiles(t *testing.T) {
	resp := NewCodeGenResponse("test", nil)
	cmd := PluginCommand{Path: os.Args[0]}
	err := ExecCommand(context.Background(), cmd, &CodeGenRequest{}, resp)
	if err == nil {
		t.Fatal("expected an error when no files are given")
	}
	if !strings.Contains(err.Error(), "nothing to generate") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPluginCommandName(t *testing.T) {
	testCases := []struct {
		cmd      PluginCommand
		expected string
	}{
		{PluginCommand{Path: "/usr/local/bin/protoc-gen-go"}, "go"},
		{PluginCommand{Path: "/usr/local/bin/mygen"}, "mygen"},
		{PluginCommand{Name: "go", Path: "/usr/bin/go", Args: []string{"tool", "x"}}, "go"},
		// An explicitly given name is used as-is.
		{PluginCommand{Name: "protoc-gen-foo", Path: "/usr/bin/go"}, "protoc-gen-foo"},
	}
	for _, testCase := range testCases {
		if got := testCase.cmd.name(); got != testCase.expected {
			t.Errorf("name of %+v: expected %q, got %q", testCase.cmd, testCase.expected, got)
		}
	}
}
