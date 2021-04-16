package plugins

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	dpb "github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/jhump/gopoet"
	"github.com/jhump/protoreflect/desc"
)

// GoNames is a helper for computing the names and types of Go elements that are
// generated from protocol buffers.
//
// GoNames is not thread-safe.
type GoNames struct {
	// A user-provided map of proto file names to the Go import package where
	// that file's code is generated. These mappings can be specified via
	// "M<protofile>=<gopkg>" args to the "--go_out" protoc argument. Other
	// plugins that generate Go code may support the option, too.
	ImportMap map[string]string

	// The import path prefix that matches the root of the current Go module.
	// When non-empty, this will be stripped from paths returned from the
	// OutputFilenameFor. This path can provided as a "module=<path>" arg to
	// the "--go_out" protoc argument. Other plugins that generate Go code may
	// support this option, too.
	ModuleRoot string

	// When true, output paths computed by OutputFilenameFor using the path
	// of the input source file instead of using its corresponding Go import
	// path. This can be enabled using a "paths=source_relative" arg to the
	// "--go_out" protoc argument. Other plugins that generate Go code may
	// support this option, too.
	//
	// If this flag is true, the ModuleRoot field is ignored.
	SourceRelative bool

	// cache of descriptor to TypeName
	descTypes map[typeKey]gopoet.TypeName
	// cache of descriptor to names
	descNames map[nameKey]string
	// cache of extension field descriptors to symbol representing generated var
	extSymbols map[*desc.FieldDescriptor]gopoet.Symbol
	// cache of file descriptor to Package
	pkgNames map[*desc.FileDescriptor]gopoet.Package
}

type typeKeyKind int

const (
	// Default indicates the type that directly corresponds to a descriptor, for
	// messages and enums. For fields, it returns the field's data type.
	typeKeyDefault typeKeyKind = iota
	// For fields, this is the type returned by the accessor method. For syntax
	// proto2 files, it can differ from the actual field type since this will
	// return primitive types, but the actual fields are pointers.
	typeKeyAccessor
	// For services and methods, this kind is used for the client-side interfaces.
	typeKeyClient
	// For services and methods, this kind is used for the server-side interfaces.
	typeKeyServer
	// For fields in a oneof, this will be used for the generated single-field structs.
	typeKeyOneOfField
)

type typeKey struct {
	d desc.Descriptor
	k typeKeyKind
}

type nameKeyKind int

const (
	// Default indicates the name of a field or method for a descriptor.
	nameKeyDefault nameKeyKind = iota
	// This is used for the name of the unexported interface that corresponds
	// to a oneof.
	nameKeyOneofInterface
	// This is for the unexported implementation of a service stub interface.
	nameKeyServiceImplClient
	// This is for the unexported/legacy grpc.ServiceDesc var.
	nameKeyServiceDesc
	// This is for the exported/newer grpc.ServiceDesc var.
	nameKeyExportedServiceDesc
	// This is for the unexported implementation of a client-side stream.
	nameKeyMethodStreamImplClient
	// This is for the unexported implementation of a server-side stream.
	nameKeyMethodStreamImplServer
)

type nameKey struct {
	d desc.Descriptor
	k nameKeyKind
}

// OutputFilenameFor computes an output filename for the given file that has the
// given suffix/extension. The name includes a path relative to the plugin's
// output. This method is used to construct the path and name of a file that a
// protoc plugin will generate.
//
// For example, querying for the suffix ".pb.go" will result in the filename
// created by the protoc-gen-go plugin.
func (n *GoNames) OutputFilenameFor(fd *desc.FileDescriptor, suffix string) string {
	var outputPath string
	if n.SourceRelative {
		outputPath = filepath.Dir(fd.GetName())
	} else {
		outputPath = n.GoPackageForFile(fd).ImportPath
		if n.ModuleRoot != "" {
			root := n.ModuleRoot
			if !strings.HasSuffix(root, "/") {
				root = root + "/"
			}
			outputPath = strings.TrimPrefix(outputPath, root)
		}
	}

	name := filepath.Base(fd.GetName())
	if ext := path.Ext(name); ext == ".proto" || ext == ".protodevel" {
		name = name[:len(name)-len(ext)]
	}
	name += suffix

	return path.Join(outputPath, name)
}

// GoPackageForFile returns the Go package for the given file descriptor. This will use
// the file's "go_package" option if it has one, but that can be overridden if the user
// has supplied an entry in n.ImportMap.
func (n *GoNames) GoPackageForFile(fd *desc.FileDescriptor) gopoet.Package {
	return n.GoPackageForFileWithOverride(fd, "")
}

// GoPackageForFileWithOverride returns the Go package for the given file descriptor,
// but uses the given string as if it were the "go_package" option value.
func (n *GoNames) GoPackageForFileWithOverride(fd *desc.FileDescriptor, goPackage string) gopoet.Package {
	if pkg, ok := n.pkgNames[fd]; ok {
		return pkg
	}

	// if not supplied: get go_package option from file, but allow it to
	// be overridden by user-supplied import map
	if goPackage == "" {
		var ok bool
		goPackage, ok = n.ImportMap[fd.GetName()]
		if !ok {
			goPackage = fd.GetFileOptions().GetGoPackage()
		}
	}

	fileName, protoPackage := fd.GetName(), fd.GetPackage()
	var pkgPath, pkgName string
	if goPackage == "" {
		pkgPath = path.Dir(fileName)
		if protoPackage == "" {
			n := path.Base(fileName)
			ext := path.Ext(n)
			if ext == "" || len(ext) == len(n) {
				pkgName = n
			} else {
				pkgName = n[:len(n)-len(ext)]
			}
		} else {
			pkgName = protoPackage
		}
	} else {
		parts := strings.Split(goPackage, ";")
		if len(parts) > 1 {
			pkgPath = parts[0]
			pkgName = parts[1]
		} else {
			pkgName = path.Base(parts[0])
			if strings.Contains(parts[0], "/") {
				pkgPath = parts[0]
			} else {
				pkgPath = path.Dir(fileName)
			}
		}
	}
	pkgName = sanitize(pkgName)

	pkg := gopoet.Package{ImportPath: pkgPath, Name: pkgName}
	if n.pkgNames == nil {
		n.pkgNames = map[*desc.FileDescriptor]gopoet.Package{}
	}
	n.pkgNames[fd] = pkg
	return pkg
}

func sanitize(name string) string {
	var buf bytes.Buffer
	for i, ch := range name {
		switch {
		case unicode.IsDigit(ch):
			if i == 0 {
				buf.WriteRune('_')
			}
			buf.WriteRune(ch)
		case unicode.IsLetter(ch):
			buf.WriteRune(ch)
		default:
			buf.WriteRune('_')
		}
	}
	return buf.String()
}

// GoTypeForMessage returns the Go type for the given message descriptor.
func (n *GoNames) GoTypeForMessage(md *desc.MessageDescriptor) gopoet.TypeName {
	return n.goTypeFor(md)
}

// GoTypeForEnum returns the Go type for the given enum descriptor.
func (n *GoNames) GoTypeForEnum(ed *desc.EnumDescriptor) gopoet.TypeName {
	return n.goTypeFor(ed)
}

func (n *GoNames) goTypeFor(d desc.Descriptor) gopoet.TypeName {
	return n.getOrComputeAndStoreType(typeKey{d: d, k: typeKeyDefault}, func() gopoet.TypeName {
		return gopoet.NamedType(n.goSymbolFor(d))
	})
}

func (n *GoNames) goSymbolFor(d desc.Descriptor) gopoet.Symbol {
	l := 0
	for parent := d; !isFile(parent); parent = parent.GetParent() {
		l++
	}
	s := make([]string, l)
	for parent := d; !isFile(parent); parent = parent.GetParent() {
		l--
		s[l] = parent.GetName()
	}
	return n.GoPackageForFile(d.GetFile()).Symbol(camelCaseSlice(s))
}

func isFile(d desc.Descriptor) bool {
	_, ok := d.(*desc.FileDescriptor)
	return ok
}

// GoTypeForOneof returns the unexported name of the Go interface type for the
// given oneof descriptor. This interface has numerous types that implement it,
// each of which can be determined using GoTypeNameForOneofField with the
// various fields that belong to the oneof.
//
// This does not return a *TypeName because the type is not usable outside of
// the generated package due to the interface being unexported. So only the
// interface's unqualified name is useful.
func (n *GoNames) GoTypeForOneof(ood *desc.OneOfDescriptor) string {
	return n.getOrComputeName(nameKey{d: ood, k: nameKeyOneofInterface}, func() {
		n.computeMessage(ood.GetOwner())
	})
}

// GoTypeForOneofChoice returns the single-field struct type that contains the
// given oneof choice. The returned type implements the oneof interface type
// named by GoTypeForOneof.
func (n *GoNames) GoTypeForOneofChoice(fld *desc.FieldDescriptor) gopoet.TypeName {
	if fld.GetOneOf() == nil {
		panic(fmt.Sprintf("field %s is not part of a oneof", fld.GetFullyQualifiedName()))
	}
	return n.getOrComputeType(typeKey{d: fld, k: typeKeyOneOfField}, func() {
		n.computeMessage(fld.GetOwner())
	})
}

// GoNameOfField returns the name of the field for the given field descriptor.
// This will name a field in a message struct or in a single-field struct that
// satisfies a oneof interface if this field is part of a oneof.
func (n *GoNames) GoNameOfField(fld *desc.FieldDescriptor) string {
	if fld.IsExtension() {
		panic(fmt.Sprintf("field %s is an extension", fld.GetFullyQualifiedName()))
	}
	return n.getOrComputeName(nameKey{d: fld, k: nameKeyDefault}, func() {
		n.computeMessage(fld.GetOwner())
	})
}

// GoNameOfOneOf returns the name of the field for the given oneof descriptor.
func (n *GoNames) GoNameOfOneOf(ood *desc.OneOfDescriptor) string {
	return n.getOrComputeName(nameKey{d: ood, k: nameKeyDefault}, func() {
		n.computeMessage(ood.GetOwner())
	})
}

// GoNameOfEnumVal returns the name of the constant that represents the given
// enum value descriptor.
func (n *GoNames) GoNameOfEnumVal(evd *desc.EnumValueDescriptor) gopoet.Symbol {
	name := fmt.Sprintf("%s_%s", n.CamelCase(evd.GetParent().GetName()), evd.GetName())
	return n.GoPackageForFile(evd.GetFile()).Symbol(name)
}

// GoNameOfExtensionDesc returns the name of the *proto.ExtensionDesc var that
// represents the given extension field descriptor.
func (n *GoNames) GoNameOfExtensionDesc(fld *desc.FieldDescriptor) gopoet.Symbol {
	if !fld.IsExtension() {
		panic(fmt.Sprintf("field %s is not an extension", fld.GetFullyQualifiedName()))
	}

	if s, ok := n.extSymbols[fld]; ok {
		return s
	}

	sym := n.goSymbolFor(fld)
	sym.Name = "E_" + sym.Name
	if n.extSymbols == nil {
		n.extSymbols = map[*desc.FieldDescriptor]gopoet.Symbol{}
	}
	n.extSymbols[fld] = sym
	return sym
}

// GoTypeOfField returns the Go type of the given field descriptor. This will
// be the type of the generated field. If fld is an extension, it is the type
// of allowed values for the extension.
func (n *GoNames) GoTypeOfField(fld *desc.FieldDescriptor) gopoet.TypeName {
	return n.getOrComputeType(typeKey{d: fld, k: typeKeyDefault}, func() {
		n.computeTypeOfFieldLocked(fld)
	})
}

// GoTypeOfFieldAccessor returns the Go type of the given field accessor.
func (n *GoNames) GoTypeOfFieldAccessor(fld *desc.FieldDescriptor) gopoet.TypeName {
	return n.getOrComputeType(typeKey{d: fld, k: typeKeyAccessor}, func() {
		n.computeTypeOfFieldLocked(fld)
	})
}

var bytesType = gopoet.SliceType(gopoet.ByteType)

func (n *GoNames) computeTypeOfFieldLocked(fld *desc.FieldDescriptor) {
	if fld.IsMap() {
		kt := n.GoTypeOfField(fld.GetMapKeyType())
		vt := n.GoTypeOfField(fld.GetMapValueType())
		if kt.Kind() == gopoet.KindPtr && kt.Elem().Kind() == gopoet.KindBasic {
			kt = kt.Elem()
		}
		if vt.Kind() == gopoet.KindPtr && vt.Elem().Kind() == gopoet.KindBasic {
			vt = vt.Elem()
		}
		t := gopoet.MapType(kt, vt)
		n.descTypes[typeKey{d: fld, k: typeKeyDefault}] = t
		n.descTypes[typeKey{d: fld, k: typeKeyAccessor}] = t
		return
	}

	var t gopoet.TypeName
	switch fld.GetType() {
	case dpb.FieldDescriptorProto_TYPE_BOOL:
		t = gopoet.BoolType
	case dpb.FieldDescriptorProto_TYPE_STRING:
		t = gopoet.StringType
	case dpb.FieldDescriptorProto_TYPE_BYTES:
		t = bytesType
	case dpb.FieldDescriptorProto_TYPE_INT32,
		dpb.FieldDescriptorProto_TYPE_SINT32,
		dpb.FieldDescriptorProto_TYPE_SFIXED32:
		t = gopoet.Int32Type
	case dpb.FieldDescriptorProto_TYPE_INT64,
		dpb.FieldDescriptorProto_TYPE_SINT64,
		dpb.FieldDescriptorProto_TYPE_SFIXED64:
		t = gopoet.Int64Type
	case dpb.FieldDescriptorProto_TYPE_UINT32,
		dpb.FieldDescriptorProto_TYPE_FIXED32:
		t = gopoet.Uint32Type
	case dpb.FieldDescriptorProto_TYPE_UINT64,
		dpb.FieldDescriptorProto_TYPE_FIXED64:
		t = gopoet.Uint64Type
	case dpb.FieldDescriptorProto_TYPE_FLOAT:
		t = gopoet.Float32Type
	case dpb.FieldDescriptorProto_TYPE_DOUBLE:
		t = gopoet.Float64Type
	case dpb.FieldDescriptorProto_TYPE_GROUP,
		dpb.FieldDescriptorProto_TYPE_MESSAGE:
		t = gopoet.PointerType(n.GoTypeForMessage(fld.GetMessageType()))
	case dpb.FieldDescriptorProto_TYPE_ENUM:
		t = n.GoTypeForEnum(fld.GetEnumType())
	default:
		panic(fmt.Sprintf("unrecognized type: %v", fld.GetType()))
	}

	if fld.IsRepeated() {
		t = gopoet.SliceType(t)
	}
	n.descTypes[typeKey{d: fld, k: typeKeyAccessor}] = t

	if !fld.GetFile().IsProto3() && t.Kind() != gopoet.KindPtr && t.Kind() != gopoet.KindSlice {
		// for proto2, type is pointer or slice
		n.descTypes[typeKey{d: fld, k: typeKeyDefault}] = gopoet.PointerType(t)
	} else {
		// otherwise, field and accessor types are the same
		n.descTypes[typeKey{d: fld, k: typeKeyDefault}] = t
	}
}

// GoTypeForServiceClient returns the Go type of the generated client interface
// for the given service descriptor.
func (n *GoNames) GoTypeForServiceClient(sd *desc.ServiceDescriptor) gopoet.TypeName {
	return n.getOrComputeType(typeKey{d: sd, k: typeKeyClient}, func() {
		n.computeService(sd)
	})
}

// GoTypeForServiceServer returns the Go type of the generated server interface
// for the given service descriptor.
func (n *GoNames) GoTypeForServiceServer(sd *desc.ServiceDescriptor) gopoet.TypeName {
	return n.getOrComputeType(typeKey{d: sd, k: typeKeyServer}, func() {
		n.computeService(sd)
	})
}

// GoTypeForServiceClientImpl returns the unexported name of the Go struct type
// that provides the default/generated implementation of the client interface
// for the given service descriptor.
//
// This does not return a *TypeName because the type is not usable outside of
// the generated package due to the struct being unexported. So only the
// struct's unqualified name is useful.
func (n *GoNames) GoTypeForServiceClientImpl(sd *desc.ServiceDescriptor) string {
	return n.getOrComputeName(nameKey{d: sd, k: nameKeyServiceImplClient}, func() {
		n.computeService(sd)
	})
}

// GoNameOfExportedServiceDesc returns the newer exported name of the var that
// holds the grpc.ServiceDesc that describes the given service. As of v1.0 of
// protoc-gen-go-grpc, this var is exported.
//
// If generating code that needs to reference the older, unexported var (from
// earlier versions of protoc plugins for Go), use GoNameOfServiceDesc instead.
func (n *GoNames) GoNameOfExportedServiceDesc(sd *desc.ServiceDescriptor) gopoet.Symbol {
	name := n.getOrComputeName(nameKey{d: sd, k: nameKeyExportedServiceDesc}, func() {
		n.computeService(sd)
	})
	return n.GoPackageForFile(sd.GetFile()).Symbol(name)
}

// GoNameOfServiceDesc returns the legacy unexported name of the var that
// holds the grpc.ServiceDesc that describes the given service. Prior to v1.0
// and of protoc-gen-go-grpc, for generating Go code related to gRPC, these
// variables were unexported.
//
// This does not return a gopoet.Symbol because the var is not usable outside
// of the generated package due to its being unexported. So only the symbol's
// unqualified name is useful.
func (n *GoNames) GoNameOfServiceDesc(sd *desc.ServiceDescriptor) string {
	return n.getOrComputeName(nameKey{d: sd, k: nameKeyServiceDesc}, func() {
		n.computeService(sd)
	})
}

// GoNameOfMethod returns the name of the Go method corresponding to the given
// method descriptor. This will be the name of a method in the generated client
// and server interfaces. (The interfaces will have different signatures for
// client vs. server, but the same method names.)
func (n *GoNames) GoNameOfMethod(md *desc.MethodDescriptor) string {
	return n.getOrComputeName(nameKey{d: md, k: nameKeyDefault}, func() {
		n.computeService(md.GetService())
	})
}

// GoTypeForStreamClient returns the Go type of the generated client stream
// interface for the given method descriptor. The given method must not be a
// unary method.
func (n *GoNames) GoTypeForStreamClient(md *desc.MethodDescriptor) gopoet.TypeName {
	return n.getOrComputeType(typeKey{d: md, k: typeKeyClient}, func() {
		n.computeService(md.GetService())
	})
}

// GoTypeForStreamServer returns the Go type of the generated server stream
// interface for the given method descriptor. The given method must not be a
// unary method.
func (n *GoNames) GoTypeForStreamServer(md *desc.MethodDescriptor) gopoet.TypeName {
	return n.getOrComputeType(typeKey{d: md, k: typeKeyClient}, func() {
		n.computeService(md.GetService())
	})
}

// GoTypeForStreamClientImpl returns the unexported name of the Go struct type
// that provides the default/generated implementation of the client stream
// interface for the given method descriptor.
//
// This does not return a *TypeName because the type is not usable outside of
// the generated package due to the struct being unexported. So only the
// struct's unqualified name is useful.
func (n *GoNames) GoTypeForStreamClientImpl(md *desc.MethodDescriptor) string {
	return n.getOrComputeName(nameKey{d: md, k: nameKeyMethodStreamImplClient}, func() {
		n.computeService(md.GetService())
	})
}

// GoTypeForStreamServerImpl returns the unexported name of the Go struct type
// that provides the default/generated implementation of the server stream
// interface for the given method descriptor.
//
// This does not return a *TypeName because the type is not usable outside of
// the generated package due to the struct being unexported. So only the
// struct's unqualified name is useful.
func (n *GoNames) GoTypeForStreamServerImpl(md *desc.MethodDescriptor) string {
	return n.getOrComputeName(nameKey{d: md, k: nameKeyMethodStreamImplServer}, func() {
		n.computeService(md.GetService())
	})
}

// GoTypeOfRequest returns the Go type of the request.
func (n *GoNames) GoTypeOfRequest(md *desc.MethodDescriptor) gopoet.TypeName {
	return gopoet.PointerType(n.GoTypeForMessage(md.GetInputType()))
}

// GoTypeOfResponse returns the Go type of the response.
func (n *GoNames) GoTypeOfResponse(md *desc.MethodDescriptor) gopoet.TypeName {
	return gopoet.PointerType(n.GoTypeForMessage(md.GetOutputType()))
}

// CamelCase converts the given symbol to an exported Go symbol in camel-case
// convention. It removes underscores and makes letters following an underscore
// upper-case. If the given symbol starts with an underscore, the underscore is
// replaced with a capital "X".
func CamelCase(s string) string {
	// NB(jh): This is forked from generator.CamelCase in the protobuf runtime.
	// It is forked to avoid deprecation warnings. That entire package is now
	// deprecated and even prints warnings in its init function(!!). But its
	// replacement ("protogen" in the google.golang.org/protobuf module) does
	// not have an analog for this function.
	if s == "" {
		return ""
	}
	t := make([]byte, 0, 32)
	i := 0
	if s[0] == '_' {
		// Need a capital letter; drop the '_'.
		t = append(t, 'X')
		i++
	}
	// Invariant: if the next letter is lower case, it must be converted
	// to upper case.
	// That is, we process a word at a time, where words are marked by _ or
	// upper case letter. Digits are treated as words.
	for ; i < len(s); i++ {
		c := s[i]
		if c == '_' && i+1 < len(s) && isASCIILower(s[i+1]) {
			continue // Skip the underscore in s.
		}
		if isASCIIDigit(c) {
			t = append(t, c)
			continue
		}
		// Assume we have a letter now - if not, it's a bogus identifier.
		// The next word is a sequence of characters that must start upper case.
		if isASCIILower(c) {
			c ^= ' ' // Make it a capital letter.
		}
		t = append(t, c) // Guaranteed not lower case.
		// Accept lower case sequence that follows.
		for i+1 < len(s) && isASCIILower(s[i+1]) {
			i++
			t = append(t, s[i])
		}
	}
	return string(t)
}

// camelCaseSlice is like CamelCase, but the argument is a slice of strings to
// be joined with "_".
func camelCaseSlice(elem []string) string {
	return CamelCase(strings.Join(elem, "_"))
}

// Is c an ASCII lower-case letter?
func isASCIILower(c byte) bool {
	return 'a' <= c && c <= 'z'
}

// Is c an ASCII digit?
func isASCIIDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

func (n *GoNames) getOrComputeAndStoreType(key typeKey, compute func() gopoet.TypeName) gopoet.TypeName {
	return n.getOrComputeType(key, func() {
		n.descTypes[key] = compute()
	})
}

func (n *GoNames) getOrComputeType(key typeKey, compute func()) gopoet.TypeName {
	if tn, ok := n.descTypes[key]; ok {
		return tn
	}

	if n.descTypes == nil {
		n.descTypes = map[typeKey]gopoet.TypeName{}
	}
	if n.descNames == nil {
		n.descNames = map[nameKey]string{}
	}
	compute()
	return n.descTypes[key]
}

func (n *GoNames) getOrComputeName(key nameKey, compute func()) string {
	if n, ok := n.descNames[key]; ok {
		return n
	}

	if n.descTypes == nil {
		n.descTypes = map[typeKey]gopoet.TypeName{}
	}
	if n.descNames == nil {
		n.descNames = map[nameKey]string{}
	}
	compute()
	return n.descNames[key]
}

// CamelCase converts the given identifier to an exported name that uses
// camel-case. Interior underscores followed by lower-case letters will be
// changed to upper-case letters. If the name begins with an underscore, it will
// be replaced with "X" so the resulting identifier is exported without
// colliding with similar identifier that does not being with an underscore.
func (n *GoNames) CamelCase(name string) string {
	return CamelCase(name)
}

func (n *GoNames) computeService(sd *desc.ServiceDescriptor) {
	exportedSvr := CamelCase(sd.GetName())
	unexportedSvr := gopoet.Unexport(exportedSvr)
	pkg := n.GoPackageForFile(sd.GetFile())

	n.descTypes[typeKey{d: sd, k: typeKeyClient}] = gopoet.NamedType(pkg.Symbol(exportedSvr + "Client"))
	n.descTypes[typeKey{d: sd, k: typeKeyServer}] = gopoet.NamedType(pkg.Symbol(exportedSvr + "Server"))
	n.descNames[nameKey{d: sd, k: nameKeyServiceImplClient}] = unexportedSvr + "Client"
	n.descNames[nameKey{d: sd, k: nameKeyServiceDesc}] = "_" + exportedSvr + "_serviceDesc"
	n.descNames[nameKey{d: sd, k: nameKeyExportedServiceDesc}] = exportedSvr + "_ServiceDesc"

	for _, mtd := range sd.GetMethods() {
		mtdName := CamelCase(mtd.GetName())
		n.descNames[nameKey{d: mtd, k: nameKeyDefault}] = mtdName

		if !mtd.IsClientStreaming() && !mtd.IsServerStreaming() {
			// no stream info for unary methods
			continue
		}

		exportedStream := exportedSvr + "_" + mtdName
		unexportedStream := unexportedSvr + mtdName
		n.descTypes[typeKey{d: mtd, k: typeKeyClient}] = gopoet.NamedType(pkg.Symbol(exportedStream + "Client"))
		n.descTypes[typeKey{d: mtd, k: typeKeyServer}] = gopoet.NamedType(pkg.Symbol(exportedStream + "Server"))
		n.descNames[nameKey{d: mtd, k: nameKeyMethodStreamImplClient}] = unexportedStream + "Client"
		n.descNames[nameKey{d: mtd, k: nameKeyMethodStreamImplServer}] = unexportedStream + "Server"
	}
}

var reservedMethodNames = [...]string{
	"Reset",
	"String",
	"ProtoMessage",
	"Marshal",
	"Unmarshal",
	"ExtensionRangeArray",
	"ExtensionMap",
	"Descriptor",
}

func (n *GoNames) computeMessage(md *desc.MessageDescriptor) {
	// mirrors the logic in protoc-gen-go to assign names to
	// fields, oneofs, and associated types
	usedNames := map[string]bool{}
	for _, n := range reservedMethodNames {
		usedNames[n] = true
	}
	computedOneOfs := map[*desc.OneOfDescriptor]bool{}
	msgType := n.GoTypeForMessage(md).Symbol()
	pkg := msgType.Package
	msgName := msgType.Name

	for _, fld := range md.GetFields() {
		fldName := CamelCase(fld.GetName())
		for {
			if _, ok := usedNames[fldName]; ok {
				fldName = fldName + "_"
				continue
			}
			if _, ok := usedNames["Get"+fldName]; ok {
				fldName = fldName + "_"
				continue
			}
			break
		}
		usedNames[fldName] = true

		n.descNames[nameKey{d: fld, k: nameKeyDefault}] = fldName
		ood := fld.GetOneOf()
		if ood != nil && !computedOneOfs[ood] {
			oodName := CamelCase(ood.GetName())
			for {
				if _, ok := usedNames[oodName]; ok {
					oodName = oodName + "_"
					continue
				}
				break
			}
			usedNames[oodName] = true

			n.descNames[nameKey{d: ood, k: nameKeyDefault}] = oodName
			n.descNames[nameKey{d: ood, k: nameKeyOneofInterface}] = "is" + msgName + "_" + oodName

			oneofFieldName := msgName + "_" + fldName
			for {
				ok := true
				for _, nmd := range md.GetNestedMessageTypes() {
					if n.GoTypeForMessage(nmd).Symbol().Name == oneofFieldName {
						ok = false
						break
					}
				}
				if ok {
					for _, ed := range md.GetNestedEnumTypes() {
						if n.GoTypeForEnum(ed).Symbol().Name == oneofFieldName {
							ok = false
							break
						}
					}
				}
				if ok {
					break
				}
				oneofFieldName = oneofFieldName + "_"
			}
			n.descTypes[typeKey{d: fld, k: typeKeyOneOfField}] = gopoet.NamedType(pkg.Symbol(oneofFieldName))

			computedOneOfs[ood] = true
		}
	}
}
