package plugins

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"sync"

	"github.com/jhump/protoreflect/desc"
)

// Plugin is a code generator that generates code during protoc invocations.
// Multiple plugins can be run during the same protoc invocation.
type Plugin func(*CodeGenRequest, *CodeGenResponse) error

// Package is a simple representation of a Go package. The name may actually be
// an effective alias (when the package is imported using an alias). A symbol
// whose package has an empty Name is a local symbol: in the same package as the
// referencing context (and thus needs no package prefix to reference that
// symbol).
type Package struct {
	ImportPath, Name string
}

// Symbol references an element in Go source. It is a const, var, function, or
// type.
type Symbol struct {
	Package Package
	Name    string
}

// String prints the symbol as it should appear in Go source: pkg.Name. The
// "pkg." prefix will be omitted if the symbol's Package has an empty Name.
func (s *Symbol) String() string {
	if s.Package.Name != "" {
		return s.Package.Name + "." + s.Name
	}
	return s.Name
}

type importDef struct {
	packageName string
	isAlias     bool
}

// Imports accumulate a set of package imports, used for generating a Go source
// file and accumulating references to other packages. As packages are imported,
// they will be assigned aliases if necessary (e.g. two imported packages
// otherwise would have the same name/prefix).
//
// Imports is not thread-safe.
type Imports struct {
	pkgPath       string
	importsByPath map[string]importDef
	pathsByName   map[string]string
}

// NewImportsFor returns a new Imports where the source lives in pkgPath. So any
// uses of other symbols also in pkgPath will not need an import and will not
// use a package prefix (see EnsureImported).
func NewImportsFor(pkgPath string) *Imports {
	return &Imports{pkgPath: pkgPath}
}

// RegisterImportForPackage "imports" the specified package and returns the
// package prefix to use for symbols in the imported package. See
// RegisterImport for more details.
func (i *Imports) RegisterImportForPackage(pkg Package) string {
	return i.RegisterImport(pkg.ImportPath, pkg.Name)
}

// RegisterImport "imports" the specified package and returns the package prefix
// to use for symbols in the imported package. It is safe to import the same
// package repeatedly -- the same prefix will be returned every time. If an
// attempt is made to import the Imports source package (i.e. importing a
// package into itself), nothing will be done and an empty prefix will be
// returned. So such an action is safe and the returned prefix is correct for
// how symbols in the package should be referenced.
func (i *Imports) RegisterImport(importPath, packageName string) string {
	return i.prefixForPackage(importPath, packageName, true)
}

// PrefixForPackage returns a prefix to use for qualifying symbols from the
// given package. This method panics if the given package was never registered.
func (i *Imports) PrefixForPackage(importPath string) string {
	return i.prefixForPackage(importPath, "", false)
}

func (i *Imports) prefixForPackage(importPath, packageName string, registerIfNotFound bool) string {
	if importPath == i.pkgPath {
		return ""
	}
	if ex, ok := i.importsByPath[importPath]; ok {
		return ex.packageName + "."
	}

	if !registerIfNotFound {
		panic(fmt.Sprintf("Package %q never registered", importPath))
	}

	p := packageName
	if packageName == "" {
		p = path.Base(importPath)
	}
	suffix := 1
	for {
		if _, ok := i.pathsByName[p]; !ok {
			if i.importsByPath == nil {
				i.importsByPath = map[string]importDef{}
				i.pathsByName = map[string]string{}
			}
			i.pathsByName[p] = importPath
			i.importsByPath[importPath] = importDef{
				packageName: p,
				isAlias:     p != packageName,
			}
			return p + "."
		}
		p = fmt.Sprintf("%s%d", packageName, suffix)
		suffix++
	}
}

// EnsureImported ensures that the given symbol is imported and returns a new
// symbol that has the correct package prefix (based on how the given symbol's
// package was imported/aliased). If the symbol is already in Imports source
// package then a symbol is returned whose Package has an empty Name. That way
// calling String() on the returned symbol will correctly elide the package
// prefix.
func (i *Imports) EnsureImported(sym *Symbol) *Symbol {
	return i.qualify(sym, true)
}

func (i *Imports) Qualify(sym *Symbol) *Symbol {
	return i.qualify(sym, false)
}

func (i *Imports) qualify(sym *Symbol, registerIfNotFound bool) *Symbol {
	name := i.prefixForPackage(sym.Package.ImportPath, sym.Package.Name, registerIfNotFound)
	if len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	if name != sym.Package.Name {
		var pkg Package
		if name != "" {
			pkg = Package{Name: name, ImportPath: sym.Package.ImportPath}
		}
		return &Symbol{Package: pkg, Name: sym.Name}
	}
	return sym
}

// EnsureTypeImported ensures that any symbols referenced by the given type are
// imported and returns a new type with correct package prefixes. See
// EnsureImported for more details.
func (i *Imports) EnsureTypeImported(n *TypeName) *TypeName {
	return i.qualifyType(n, true)
}

func (i *Imports) QualifyType(n *TypeName) *TypeName {
	return i.qualifyType(n, false)
}

func (i *Imports) qualifyType(n *TypeName, registerIfNotFound bool) *TypeName {
	switch n.Kind() {
	case KindPtr:
		elem := n.Elem()
		nelem := i.qualifyType(n.Elem(), registerIfNotFound)
		if nelem != elem {
			n = ptrType(nelem)
		}
	case KindSlice:
		elem := n.Elem()
		nelem := i.qualifyType(n.Elem(), registerIfNotFound)
		if nelem != elem {
			n = sliceType(nelem)
		}
	case KindMap:
		key := n.Key()
		elem := n.Elem()
		nkey := i.qualifyType(n.Key(), registerIfNotFound)
		nelem := i.qualifyType(n.Elem(), registerIfNotFound)
		if nelem != elem || nkey != key {
			n = mapType(nkey, nelem)
		}
	case KindNamed:
		sym := n.Symbol()
		nsym := i.qualify(sym, registerIfNotFound)
		if nsym != sym {
			n = namedType(nsym)
		}
	}
	return n
}

// ImportSpecs returns the list of imports that have been accumulated so far,
// sorted lexically by import path.
func (i *Imports) ImportSpecs() []ImportSpec {
	specs := make([]ImportSpec, len(i.importsByPath))
	idx := 0
	for importPath, def := range i.importsByPath {
		specs[idx].ImportPath = importPath
		if def.isAlias {
			specs[idx].PackageAlias = def.packageName
		}
		idx++
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].ImportPath < specs[j].ImportPath
	})
	return specs
}

// ImportSpec describes an import statement in Go source. The spec's
// PackageAlias will be empty if the import statement needs no alias.
type ImportSpec struct {
	PackageAlias string
	ImportPath   string
}

// CodeGenRequest represents the arguments to protoc that describe what code
// protoc has been requested to generate.
type CodeGenRequest struct {
	// Args are the parameters for the plugin.
	Args []string
	// Files are the proto source files for which code should be generated.
	Files []*desc.FileDescriptor
	// The version of protoc that has invoked the plugin.
	ProtocVersion ProtocVersion
}

// CodeGenResponse is how the plugin transmits generated code to protoc.
type CodeGenResponse struct {
	pluginName string
	output     *outputMap
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

// ProtocVersion represents a version of the protoc tool.
type ProtocVersion struct {
	Major, Minor, Patch int
	Suffix              string
}

func (v ProtocVersion) String() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%d.%d.%d", v.Major, v.Minor, v.Patch)
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
