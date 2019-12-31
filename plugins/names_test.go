package plugins

import (
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/jhump/gopoet"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/builder"
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
				SetOptions(&descriptor.FileOptions{GoPackage: proto.String("foo.com/bar")})),
			expectedResult: gopoet.Package{ImportPath: "foo.com/bar", Name: "bar"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptor.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
			expectedResult: gopoet.Package{ImportPath: "foo.com/bar", Name: "bar_pkg"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptor.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
			importMap:      map[string]string{"foo/blah.proto": "foo.io/baz"}, // not a match
			expectedResult: gopoet.Package{ImportPath: "foo.com/bar", Name: "bar_pkg"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptor.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
			importMap:      map[string]string{"foo/test.proto": "foo.io/baz"},
			expectedResult: gopoet.Package{ImportPath: "foo.io/baz", Name: "baz"},
		},
		{
			fd: mustBuildFile(builder.NewFile("foo/test.proto").
				SetPackageName("foo.bar").
				SetOptions(&descriptor.FileOptions{GoPackage: proto.String("foo.com/bar;bar_pkg")})),
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
	// TODO
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
