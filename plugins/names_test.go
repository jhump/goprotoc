package plugins

import (
	"testing"

	"github.com/jhump/gopoet"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/builder"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func mustBuildFile(f *builder.FileBuilder) *desc.FileDescriptor {
	fd, err := f.Build()
	if err != nil {
		panic(err)
	}
	return fd
}

func TestGoPackageForFile(t *testing.T) {
	testCases := []struct {
		fd             *desc.FileDescriptor
		importMap      map[string]string
		override       string
		expectedResult gopoet.Package
	}{
		{
			fd:             mustBuildFile(builder.NewFile("foo/test.proto")),
			expectedResult: gopoet.Package{ImportPath: "foo", Name: "test"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar")),
			expectedResult: gopoet.Package{ImportPath: "foo", Name: "foo_bar"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptorpb.FileOptions{GoPackage: proto.String("bar_pkg")})),
			expectedResult: gopoet.Package{ImportPath: "foo", Name: "bar_pkg"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptorpb.FileOptions{GoPackage: proto.String("foo.com/bar")})),
			expectedResult: gopoet.Package{ImportPath: "foo.com/bar", Name: "bar"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptorpb.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
			expectedResult: gopoet.Package{ImportPath: "foo.com/bar", Name: "bar_pkg"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptorpb.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
			importMap:      map[string]string{"foo/blah.proto": "foo.io/baz"}, // not a match
			expectedResult: gopoet.Package{ImportPath: "foo.com/bar", Name: "bar_pkg"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptorpb.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
			importMap:      map[string]string{"foo/test.proto": "foo.io/baz"},
			expectedResult: gopoet.Package{ImportPath: "foo.io/baz", Name: "baz"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptorpb.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
			importMap:      map[string]string{"foo/test.proto": "foo.io/baz"},
			override:       "foo.net/bar/baz;bar_baz",
			expectedResult: gopoet.Package{ImportPath: "foo.net/bar/baz", Name: "bar_baz"},
		},
	}

	for i, testCase := range testCases {
		n := GoNames{ImportMap: testCase.importMap}
		var pkg gopoet.Package
		if testCase.override != "" {
			pkg = n.GoPackageForFileWithOverride(testCase.fd, testCase.override)
		} else {
			pkg = n.GoPackageForFile(testCase.fd)
		}
		if pkg != testCase.expectedResult {
			t.Errorf("case %d: expected package %s %q, got %s %q", i, testCase.expectedResult.Name, testCase.expectedResult.ImportPath, pkg.Name, pkg.ImportPath)
		}

		if testCase.override != "" {
			// result is cached, so make sure we can get same package on
			// subsequent call, even if we don't supply the same override
			pkg2 := n.GoPackageForFile(testCase.fd)
			if pkg2 != pkg {
				t.Errorf("case %d: package with override %q not correctly cached", i, testCase.override)
			}
		}
	}
}

func TestOutputFilenameFor(t *testing.T) {
	fdNoGoOrProtoPkg := &descriptorpb.FileDescriptorProto{
		Name: proto.String("source/path/foo.proto"),
	}
	fdNoGoPkg := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("source/path/foo.proto"),
		Package: proto.String("foo.bar.com"),
	}
	fdGoPkgNameOnly := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("source/path/foo.proto"),
		Package: proto.String("foo.bar.com"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("foobar"),
		},
	}
	fdGoPkgPathOnly := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("source/path/foo.proto"),
		Package: proto.String("foo.bar.com"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/foo/bar"),
		},
	}
	fdGoPkg := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("source/path/foo.proto"),
		Package: proto.String("foo.bar.com"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/foo/bar;foobar"),
		},
	}

	namesNoOpts := &GoNames{}
	namesImportMap := &GoNames{
		ImportMap: map[string]string{"source/path/foo.proto": "github.com/fu/baz"},
	}
	namesImportMapAndModuleRoot := &GoNames{
		ImportMap:  map[string]string{"source/path/foo.proto": "github.com/fu/baz"},
		ModuleRoot: "github.com/fu",
	}
	namesModuleRoot := &GoNames{
		ModuleRoot: "github.com/foo",
	}
	namesSourceRelative := &GoNames{
		SourceRelative: true,
	}

	testCases := []struct {
		name     string
		fd       *descriptorpb.FileDescriptorProto
		n        *GoNames
		expected string
	}{
		{
			name:     "no package, no options",
			fd:       fdNoGoOrProtoPkg,
			n:        namesNoOpts,
			expected: "source/path/foo.pb.go",
		},
		{
			name:     "no package, import map",
			fd:       fdNoGoOrProtoPkg,
			n:        namesImportMap,
			expected: "github.com/fu/baz/foo.pb.go",
		},
		{
			name:     "no package, import map + module root",
			fd:       fdNoGoOrProtoPkg,
			n:        namesImportMapAndModuleRoot,
			expected: "baz/foo.pb.go",
		},
		{
			name:     "no package, source relative",
			fd:       fdNoGoOrProtoPkg,
			n:        namesSourceRelative,
			expected: "source/path/foo.pb.go",
		},

		{
			name:     "no Go package, no options",
			fd:       fdNoGoPkg,
			n:        namesNoOpts,
			expected: "source/path/foo.pb.go",
		},
		{
			name:     "no Go package, import map + module root",
			fd:       fdNoGoPkg,
			n:        namesImportMapAndModuleRoot,
			expected: "baz/foo.pb.go",
		},

		{
			name:     "Go package name, no options",
			fd:       fdGoPkgNameOnly,
			n:        namesNoOpts,
			expected: "source/path/foo.pb.go",
		},
		{
			name:     "Go package name, import map + module root",
			fd:       fdGoPkgNameOnly,
			n:        namesImportMapAndModuleRoot,
			expected: "baz/foo.pb.go",
		},

		{
			name:     "Go package path, no options",
			fd:       fdGoPkgPathOnly,
			n:        namesNoOpts,
			expected: "github.com/foo/bar/foo.pb.go",
		},
		{
			name:     "Go package path, import map + module root",
			fd:       fdGoPkgPathOnly,
			n:        namesImportMapAndModuleRoot,
			expected: "baz/foo.pb.go",
		},
		{
			name:     "Go package path, module root",
			fd:       fdGoPkgPathOnly,
			n:        namesModuleRoot,
			expected: "bar/foo.pb.go",
		},
		{
			name:     "Go package path, source relative",
			fd:       fdGoPkgPathOnly,
			n:        namesSourceRelative,
			expected: "source/path/foo.pb.go",
		},

		{
			name:     "Go package, no options",
			fd:       fdGoPkg,
			n:        namesNoOpts,
			expected: "github.com/foo/bar/foo.pb.go",
		},
		{
			name:     "Go package, import map",
			fd:       fdGoPkg,
			n:        namesImportMap,
			expected: "github.com/fu/baz/foo.pb.go",
		},
		{
			name:     "Go package, import map + module root",
			fd:       fdGoPkg,
			n:        namesImportMapAndModuleRoot,
			expected: "baz/foo.pb.go",
		},
		{
			name:     "Go package, module root",
			fd:       fdGoPkg,
			n:        namesModuleRoot,
			expected: "bar/foo.pb.go",
		},
		{
			name:     "Go package, source relative",
			fd:       fdGoPkg,
			n:        namesSourceRelative,
			expected: "source/path/foo.pb.go",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fd, err := desc.CreateFileDescriptor(testCase.fd)
			if err != nil {
				t.Errorf("failed to create descriptorpb: %v", err)
				return
			}
			filename := testCase.n.OutputFilenameFor(fd, ".pb.go")
			if filename != testCase.expected {
				t.Errorf("wrong OutputFilenameFor: expected %q, got %q", testCase.expected, filename)
			}
		})
	}
}

func TestGoNameOfField(t *testing.T) {
	// TODO
}

func TestGoNameOfEnumValue(t *testing.T) {
	// TODO
}

func TestGoNameOfMethod(t *testing.T) {
	// TODO
}

func TestGoNameOfExportedServiceDesc(t *testing.T) {
	// TODO
}

func TestGoNameOfServiceDesc(t *testing.T) {
	// TODO
}

func TestGoTypeForEnum(t *testing.T) {
	// TODO
}

func TestGoTypeForMessage(t *testing.T) {
	// TODO
}

func TestGoTypeForOneof(t *testing.T) {
	// TODO
}

func TestGoTypeForOneofChoice(t *testing.T) {
	// TODO
}

func TestGoTypeForServiceClient(t *testing.T) {
	// TODO
}

func TestGoTypeForStreamClient(t *testing.T) {
	// TODO
}

func TestGoTypeForStreamClientImpl(t *testing.T) {
	// TODO
}

func TestGoTypeForServiceServer(t *testing.T) {
	// TODO
}

func TestGoTypeForStreamServer(t *testing.T) {
	// TODO
}

func TestGoTypeForStreamServerImpl(t *testing.T) {
	// TODO
}
