package goprotoc

import (
	"io"
	"testing"
)

func TestParsePluginDef(t *testing.T) {
	testCases := []struct {
		name         string
		value        string
		expectedLang string
		expected     pluginDef
		errMsg       string
	}{
		{
			name:         "executable path, name inferred",
			value:        "/usr/local/bin/protoc-gen-go",
			expectedLang: "go",
			expected:     pluginDef{kind: pluginKindExec, loc: "/usr/local/bin/protoc-gen-go"},
		},
		{
			name:         "executable path, name given",
			value:        "protoc-gen-foo=/usr/local/bin/mygen",
			expectedLang: "foo",
			expected:     pluginDef{kind: pluginKindExec, loc: "/usr/local/bin/mygen"},
		},
		{
			name:         "go tool, name inferred",
			value:        "gotool:google.golang.org/protobuf/cmd/protoc-gen-go",
			expectedLang: "go",
			expected:     pluginDef{kind: pluginKindGoTool, loc: "google.golang.org/protobuf/cmd/protoc-gen-go"},
		},
		{
			name:         "go tool, name inferred, major version suffix ignored",
			value:        "gotool:example.com/cmd/protoc-gen-foo/v2",
			expectedLang: "foo",
			expected:     pluginDef{kind: pluginKindGoTool, loc: "example.com/cmd/protoc-gen-foo/v2"},
		},
		{
			name:         "go tool, name given",
			value:        "protoc-gen-foo=gotool:example.com/cmd/mygen",
			expectedLang: "foo",
			expected:     pluginDef{kind: pluginKindGoTool, loc: "example.com/cmd/mygen"},
		},
		{
			name:   "go tool with no package",
			value:  "gotool:",
			errMsg: "must be followed by a Go package path",
		},
		{
			name:   "go tool with version",
			value:  "gotool:google.golang.org/protobuf/cmd/protoc-gen-go@v1.33.0",
			errMsg: "must not include a version",
		},
		{
			name:   "empty location",
			value:  "protoc-gen-go=",
			errMsg: "empty location",
		},
		{
			name:   "inferred name without protoc-gen- prefix",
			value:  "gotool:example.com/cmd/mygen",
			errMsg: `should have "protoc-gen-" prefix`,
		},
		{
			name:   "given name without protoc-gen- prefix",
			value:  "foo=gotool:example.com/cmd/protoc-gen-foo",
			errMsg: `should have "protoc-gen-" prefix`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lang, def, err := parsePluginDef(testCase.value)
			if isErrorCase(t, err, testCase.errMsg) {
				return
			}
			if lang != testCase.expectedLang {
				t.Errorf("expected language %q, got %q", testCase.expectedLang, lang)
			}
			if def != testCase.expected {
				t.Errorf("expected %+v, got %+v", testCase.expected, def)
			}
		})
	}
}

// TestParseFlagsPluginDefs checks that --plugin flags accumulate into the
// options, keyed by output language.
func TestParseFlagsPluginDefs(t *testing.T) {
	args := []string{
		"--plugin=gotool:google.golang.org/protobuf/cmd/protoc-gen-go",
		"--plugin=protoc-gen-bar=/opt/bar",
	}
	expected := map[string]pluginDef{
		"go":  {kind: pluginKindGoTool, loc: "google.golang.org/protobuf/cmd/protoc-gen-go"},
		"bar": {kind: pluginKindExec, loc: "/opt/bar"},
	}

	var opts protocOptions
	if err := parseFlags("", "goprotoc", args, io.Discard, &opts, map[string]struct{}{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(opts.pluginDefs) != len(expected) {
		t.Fatalf("expected %d plugin defs, got %d: %v", len(expected), len(opts.pluginDefs), opts.pluginDefs)
	}
	for lang, want := range expected {
		if got := opts.pluginDefs[lang]; got != want {
			t.Errorf("plugin def for %q: expected %+v, got %+v", lang, want, got)
		}
	}
}
