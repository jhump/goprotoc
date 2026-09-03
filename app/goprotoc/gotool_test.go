package goprotoc

import (
	"strings"
	"testing"
)

func TestParseToolDirectives(t *testing.T) {
	testCases := []struct {
		name string
		json string
		// expected are the plugin tool directives, keyed by output language.
		expected map[string]string
		// expectedPackages are all the declared tool packages, including those
		// that are not named protoc-gen-*.
		expectedPackages []string
		errMsg           string
	}{
		{
			name:     "no tool directives",
			json:     `{"Module":{"Path":"example.com/foo"},"Go":"1.19"}`,
			expected: map[string]string{},
		},
		{
			name: "plugin tools",
			json: `{"Module":{"Path":"example.com/foo"},"Go":"1.24","Tool":[
				{"Path":"google.golang.org/protobuf/cmd/protoc-gen-go"},
				{"Path":"connectrpc.com/connect/cmd/protoc-gen-connect-go"}
			]}`,
			expected: map[string]string{
				"go":         "google.golang.org/protobuf/cmd/protoc-gen-go",
				"connect-go": "connectrpc.com/connect/cmd/protoc-gen-connect-go",
			},
			expectedPackages: []string{
				"google.golang.org/protobuf/cmd/protoc-gen-go",
				"connectrpc.com/connect/cmd/protoc-gen-connect-go",
			},
		},
		{
			name: "non-plugin tools ignored",
			json: `{"Tool":[
				{"Path":"github.com/jhump/goprotoc/cmd/goprotoc"},
				{"Path":"honnef.co/go/tools/cmd/staticcheck"},
				{"Path":"google.golang.org/protobuf/cmd/protoc-gen-go"}
			]}`,
			expected: map[string]string{
				"go": "google.golang.org/protobuf/cmd/protoc-gen-go",
			},
			// Tools that are not named protoc-gen-* are still recorded, so that
			// they can be named by an explicit --plugin flag.
			expectedPackages: []string{
				"github.com/jhump/goprotoc/cmd/goprotoc",
				"honnef.co/go/tools/cmd/staticcheck",
				"google.golang.org/protobuf/cmd/protoc-gen-go",
			},
		},
		{
			name: "major version suffix stripped",
			json: `{"Tool":[{"Path":"example.com/cmd/protoc-gen-foo/v2"}]}`,
			expected: map[string]string{
				"foo": "example.com/cmd/protoc-gen-foo/v2",
			},
			expectedPackages: []string{"example.com/cmd/protoc-gen-foo/v2"},
		},
		{
			name: "ambiguous tool names",
			json: `{"Tool":[
				{"Path":"example.com/a/protoc-gen-foo"},
				{"Path":"example.com/b/protoc-gen-foo"}
			]}`,
			errMsg: `declares more than one tool named "protoc-gen-foo"`,
		},
		{
			name:   "malformed json",
			json:   `not json`,
			errMsg: "failed to parse go.mod",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tools, err := parseToolDirectives([]byte(testCase.json))
			if isErrorCase(t, err, testCase.errMsg) {
				return
			}
			if len(tools.plugins) != len(testCase.expected) {
				t.Fatalf("expected %d plugin tools, got %d: %v", len(testCase.expected), len(tools.plugins), tools.plugins)
			}
			for lang, want := range testCase.expected {
				if got := tools.plugins[lang]; got != want {
					t.Errorf("tool for %q: expected %q, got %q", lang, want, got)
				}
			}
			if len(tools.packages) != len(testCase.expectedPackages) {
				t.Errorf("expected %d tool packages, got %d: %v", len(testCase.expectedPackages), len(tools.packages), tools.packages)
			}
			for _, pkg := range testCase.expectedPackages {
				if _, ok := tools.packages[pkg]; !ok {
					t.Errorf("package %q missing from tool packages", pkg)
				}
			}
		})
	}
}

func TestGoToolBaseName(t *testing.T) {
	testCases := map[string]string{
		"google.golang.org/protobuf/cmd/protoc-gen-go": "protoc-gen-go",
		"example.com/cmd/protoc-gen-foo/v2":            "protoc-gen-foo",
		"example.com/cmd/protoc-gen-foo/v10":           "protoc-gen-foo",
		// "vet" is not a major version suffix, so it stands on its own
		"example.com/cmd/vet": "vet",
		// nor is "v2x"
		"example.com/cmd/v2x": "v2x",
		"protoc-gen-go":       "protoc-gen-go",
	}
	for pkg, want := range testCases {
		if got := goToolBaseName(pkg); got != want {
			t.Errorf("goToolBaseName(%q): expected %q, got %q", pkg, want, got)
		}
	}
}

// isErrorCase checks err against an expected error message and reports whether
// the test case was an error case, and so needs no further checking. A test
// case with an empty errMsg expects no error.
func isErrorCase(t *testing.T, err error, errMsg string) bool {
	t.Helper()
	if errMsg == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return false
	}
	if err == nil {
		t.Fatalf("expected error containing %q, got none", errMsg)
	}
	if !strings.Contains(err.Error(), errMsg) {
		t.Fatalf("expected error containing %q, got %q", errMsg, err.Error())
	}
	return true
}
