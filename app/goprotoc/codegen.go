package goprotoc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"

	"github.com/jhump/goprotoc/plugins"
)

var protocVersionStruct = plugins.ProtocVersion{
	Major:  3,
	Minor:  5,
	Patch:  1,
	Suffix: "go",
}

var protocOutputs = map[string]struct{}{
	"cpp":      {},
	"csharp":   {},
	"java":     {},
	"javanano": {},
	"js":       {},
	"objc":     {},
	"php":      {},
	"python":   {},
	"ruby":     {},
}

type outputType int

const (
	outputTypeDir outputType = iota
	outputTypeZip
	outputTypeJar
)

// outputLocation is a location where generated code will reside. It's a directory,
// a ZIP archive, or a JAR archive; generated files will go inside. This comes
// from a --*_out argument to protoc.
type outputLocation struct {
	path         string
	locationType outputType
}

// outputFile represents a generated file. It's a pair of outputLocation and
// file name. The file name can be a path, relative to the output location.
type outputFile struct {
	loc      outputLocation
	fileName string
}

func (f outputFile) String() string {
	if f.loc.locationType == outputTypeDir {
		return filepath.Join(f.loc.path, f.fileName)
	}
	// it's a file *inside* of a zip/jar archive
	return fmt.Sprintf("%s:%s", f.loc.path, f.fileName)
}

func doCodeGen(outputs map[string]string, fds []*desc.FileDescriptor, pluginDefs map[string]string) error {
	locations, args, err := computeOutputLocations(outputs)
	if err != nil {
		return err
	}

	resps, err := runPlugins(args, fds, pluginDefs)
	if err != nil {
		return err
	}

	results, err := assembleFileOutputs(resps, locations)
	if err != nil {
		return err
	}

	// now we can accumulate outputs by archive and emit the
	// normal files
	archiveResults := map[outputLocation]map[string]io.Reader{}
	for file, data := range results {
		if file.loc.locationType == outputTypeDir {
			fileName := filepath.Join(file.loc.path, file.fileName)
			if err := writeFileResult(fileName, data); err != nil {
				return err
			}
		} else {
			archiveFiles := archiveResults[file.loc]
			if archiveFiles == nil {
				archiveFiles = map[string]io.Reader{}
				archiveResults[file.loc] = archiveFiles
			}
			archiveFiles[file.fileName] = data
		}
	}

	// finally: emit any archives
	for location, files := range archiveResults {
		if err := writeArchiveResult(location.path, location.locationType == outputTypeJar, files); err != nil {
			return err
		}
	}

	return nil
}

func computeOutputLocations(outputs map[string]string) (map[string]outputLocation, map[string]string, error) {
	locations := map[string]outputLocation{}
	args := map[string]string{}
	for lang, loc := range outputs {
		locParts := strings.SplitN(loc, ":", 2)
		var arg, dest string
		if len(locParts) > 1 {
			arg = locParts[0]
			dest = locParts[1]
		} else {
			dest = loc
		}
		if dest == "" {
			return nil, nil, fmt.Errorf("%s has empty output path", lang)
		}
		var locType outputType
		switch ext := strings.ToLower(filepath.Ext(dest)); ext {
		case ".jar":
			locType = outputTypeJar
		case ".zip":
			locType = outputTypeZip
		default:
			locType = outputTypeDir
		}

		absDest, err := filepath.Abs(dest)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compute absolute path for %s output %s: %v", lang, dest, err)
		}
		locations[lang] = outputLocation{
			path:         absDest,
			locationType: locType,
		}
		args[lang] = arg

		// Make sure given directory already exists. But if we are instructed to
		// put the files in a zip or jar, just make sure the output file's parent
		// directory exists.
		if locType != outputTypeDir {
			dest = filepath.Dir(dest)
		}
		fileInfo, err := os.Stat(dest)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, fmt.Errorf("%s: No such file or directory", dest)
			}
			return nil, nil, err
		}
		if !fileInfo.IsDir() {
			return nil, nil, fmt.Errorf("output for %s is not a directory: %s", lang, dest)
		}
	}

	return locations, args, nil
}

func runPlugins(args map[string]string, fds []*desc.FileDescriptor, pluginDefs map[string]string) (map[string]*plugins.CodeGenResponse, error) {
	req := plugins.CodeGenRequest{
		Files:         fds,
		ProtocVersion: protocVersionStruct,
	}
	resps := map[string]*plugins.CodeGenResponse{}

	for lang, arg := range args {
		resp := plugins.NewCodeGenResponse(lang, nil)
		resps[lang] = resp
		pluginName := pluginDefs[lang]
		if err := executePlugin(&req, resp, pluginName, lang, arg); err != nil {
			return nil, err
		}
	}
	return resps, nil
}

func assembleFileOutputs(resps map[string]*plugins.CodeGenResponse, locations map[string]outputLocation) (map[outputFile]io.Reader, error) {
	results := map[outputFile]fileOutput{}
	for lang, resp := range resps {
		err := resp.ForEach(func(name, insertionPoint string, data io.Reader) error {
			loc := locations[lang]
			fullOutput := outputFile{
				loc:      loc,
				fileName: name,
			}
			o := results[fullOutput]
			if insertionPoint == "" {
				if o.createdBy != "" {
					return fmt.Errorf("conflict: both %s and %s tried to create file %s", o.createdBy, lang, fullOutput)
				}
				o.contents = data
				o.createdBy = lang
			} else {
				if o.insertions == nil {
					o.insertions = map[string][]insertedContent{}
					o.insertsFrom = map[string]struct{}{}
				}
				content := insertedContent{data: data, lang: lang}
				o.insertions[insertionPoint] = append(o.insertions[insertionPoint], content)
				o.insertsFrom[lang] = struct{}{}
			}
			results[fullOutput] = o
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	resultData := map[outputFile]io.Reader{}
	for file, output := range results {
		if output.contents == nil {
			return nil, fmt.Errorf("%q generated invalid content for %s", output.createdBy, file)
		}
		fileContents := output.contents
		if len(output.insertions) > 0 {
			var err error
			fileContents, err = applyInsertions(file.String(), output.contents, output.insertions)
			if err != nil {
				return nil, err
			}
		}
		resultData[file] = fileContents
	}
	return resultData, nil
}

func writeFileResult(fileName string, data io.Reader) (e error) {
	// we've already checked that the output directory exists, but the generated
	// file could be nested inside directories therein, which we want to auto-create
	if err := os.MkdirAll(filepath.Dir(fileName), os.ModePerm); err != nil {
		return err
	}
	w, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := w.Close()
		if closeErr != nil && e == nil {
			e = closeErr
		}
	}()

	_, err = io.Copy(w, data)
	return err
}

// same manifest that protoc produces, except "goprotoc" instead of "protoc"
var manifestContents = []byte(
	`Manifest-Version: 1.0
Created-By: 1.6.0 (goprotoc)

`)

func writeArchiveResult(fileName string, includeManifest bool, files map[string]io.Reader) (e error) {
	fw, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	z := zip.NewWriter(fw)

	defer func() {
		closeErr := z.Close()
		if closeErr != nil && e == nil {
			e = closeErr
		}
		closeErr = fw.Close()
		if closeErr != nil && e == nil {
			e = closeErr
		}
	}()

	if includeManifest {
		mw, err := z.CreateHeader(&zip.FileHeader{
			Name:   "META-INF/MANIFEST.MF",
			Method: zip.Store,
		})
		if err != nil {
			return err
		}
		if _, err := mw.Write(manifestContents); err != nil {
			return err
		}
	}

	fileNames := make([]string, 0, len(files))
	for name := range files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	for _, name := range fileNames {
		w, err := z.CreateHeader(&zip.FileHeader{
			Name:   name,
			Method: zip.Store,
		})
		if err != nil {
			return err
		}
		if _, err = io.Copy(w, files[name]); err != nil {
			return err
		}
	}

	return nil
}

type fileOutput struct {
	contents    io.Reader
	createdBy   string
	insertions  map[string][]insertedContent
	insertsFrom map[string]struct{}
}

// RegisterPlugin registers the name of an in-process goprotoc plugin. The given
// name should not include the "protoc-gen-" prefix that a plugin binary name
// would have. If the command-line contains a "-plugin" argument to configure
// the named output type, that will be respected and the in-process plugin will
// not be used. This can be used to override the use of "protoc" as the source
// of code gen for Java, C++, Python, etc.
//
// If function will panic if the given name already has an in-process plugin
// registered.
//
// This function is not thread-safe. It should be invoked during program
// initialization, before other functions in this package are invoked to run
// the goprotoc tool.
func RegisterPlugin(lang string, plugin plugins.Plugin) {
	if _, ok := inprocessPlugins[lang]; ok {
		panic(fmt.Sprintf("plugin already registered for %q", lang))
	}
	inprocessPlugins[lang] = plugin
}

var inprocessPlugins = map[string]plugins.Plugin{}

func executePlugin(req *plugins.CodeGenRequest, resp *plugins.CodeGenResponse, pluginName, lang, outputArg string) error {
	if len(outputArg) > 0 {
		req.Args = strings.Split(outputArg, ",")
	}
	if pluginName == "" {
		// no configured plugin path, so first check if we have an in-process plugin
		if p, ok := inprocessPlugins[lang]; ok {
			return p(req, resp)
		}
		// maybe it's an output provided by protoc
		if _, ok := protocOutputs[lang]; ok {
			return driveProtocAsPlugin(req, resp, lang)
		}
		// otherwise, assume plugin program name by convention
		pluginName = "protoc-gen-" + lang
	}
	return plugins.Exec(context.Background(), pluginName, req, resp)
}

func driveProtocAsPlugin(req *plugins.CodeGenRequest, resp *plugins.CodeGenResponse, lang string) (err error) {
	for _, arg := range req.Args {
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("option %q for %s output does not start with '-'", arg, lang)
		}
	}

	tmpDir, err := os.MkdirTemp("", "go-protoc")
	if err != nil {
		return err
	}
	defer func() {
		cleanupErr := os.RemoveAll(tmpDir)
		if err == nil {
			err = cleanupErr
		}
	}()

	outDir := filepath.Join(tmpDir, "output")
	if err := os.Mkdir(outDir, 0700); err != nil {
		return err
	}

	fds := desc.ToFileDescriptorSet(req.Files...)
	descFile := filepath.Join(tmpDir, "descriptors")
	if fdsBytes, err := proto.Marshal(fds); err != nil {
		return err
	} else if err := os.WriteFile(descFile, fdsBytes, 0666); err != nil {
		return err
	}

	args := make([]string, 0, 2+len(req.Files)+len(req.Args))
	args = append(args, "--descriptor_set_in="+descFile)
	args = append(args, "--"+lang+"_out="+outDir)
	for _, arg := range req.Args {
		if arg == "" {
			return errors.New("request argument is empty")
		}
		args = append(args, arg)
	}
	for _, f := range req.Files {
		name := f.GetName()
		if name == "" {
			return errors.New("request filename empty")
		}
		args = append(args, name)
	}

	cmd := exec.Command("protoc", args...)
	var combinedOutput bytes.Buffer
	cmd.Stdout = &combinedOutput
	cmd.Stderr = &combinedOutput
	if err := cmd.Run(); err != nil {
		if err, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("protoc failed to produce output for %s: %v\n%s", lang, err, combinedOutput.String())
		}
		return err
	}

	return filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if (info.Mode() & os.ModeType) != 0 {
			// not a regular file
			return nil
		}
		relPath, err := filepath.Rel(outDir, path)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out := resp.OutputFile(relPath)
		if _, err := io.Copy(out, in); err != nil {
			_ = in.Close()
			return err
		}
		return in.Close()
	})
}

var insertionPointMarker = []byte("@@protoc_insertion_point(")

type insertedContent struct {
	data io.Reader
	lang string
}

func applyInsertions(fileName string, contents io.Reader, insertions map[string][]insertedContent) (io.Reader, error) {
	var result bytes.Buffer

	var data []byte
	type toBytes interface {
		Bytes() []byte
	}
	if b, ok := contents.(toBytes); ok {
		data = b.Bytes()
	} else {
		var err error
		data, err = io.ReadAll(contents)
		if err != nil {
			return nil, err
		}
	}

	for {
		pos := bytes.Index(data, insertionPointMarker)
		if pos < 0 {
			break
		}
		startPos := pos + len(insertionPointMarker)
		endPos := bytes.IndexByte(data[startPos:], ')')
		if endPos < 0 {
			// malformed marker! skip it
			break
		}
		point := string(data[startPos:endPos])
		insertedData := insertions[point]
		if len(insertedData) == 0 {
			// returned error is always nil from bytes.Buffer
			// https://golang.org/pkg/bytes/#Buffer.Write
			result.Write(data[:endPos+1])
			data = data[endPos+1:]
			continue
		}

		delete(insertions, point)

		prevLine := bytes.LastIndexByte(data[:pos], '\n')
		prevComment := bytes.LastIndexByte(data[prevLine+1:pos], '/')
		var insertionIndex int
		var sep, indent []byte
		if prevComment != -1 &&
			data[prevLine+1+prevComment+1] == '*' &&
			len(bytes.TrimSpace(data[prevLine+1+prevComment+2:pos])) == 0 {
			// insertion point preceded by "/* ", so we insert directly before
			// that with no indentation
			insertionIndex = prevLine + 1 + prevComment
			sep = []byte{' '}
		} else {
			// otherwise, insert before the insertion point line, using same
			// indent as observed on insertion point line
			insertionIndex = prevLine + 1
			sep = []byte{'\n'}
			line := data[insertionIndex:pos]
			trimmedLine := bytes.TrimLeftFunc(line, unicode.IsSpace)
			if len(line) > len(trimmedLine) {
				indent = line[:len(line)-len(trimmedLine)]
			}
		}

		result.Write(data[:insertionIndex])
		for _, ins := range insertedData {
			if len(indent) == 0 {
				if _, err := io.Copy(&result, ins.data); err != nil {
					return nil, err
				}
			} else {
				// if there's an indent, break up the inserted data
				// into lines and prefix each line with the indent
				insData, err := io.ReadAll(ins.data)
				if err != nil {
					return nil, err
				}
				lines := bytes.Split(insData, []byte{'\n'})
				for _, line := range lines {
					result.Write(indent)
					result.Write(line)
				}
			}

			if !bytes.HasSuffix(result.Bytes(), sep) {
				result.Write(sep)
			}
		}
		result.Write(data[insertionIndex : endPos+1])
		data = data[endPos+1:]
	}

	if len(insertions) > 0 {
		// gather missing insertion points by lang/plugin
		pointsByLang := map[string]map[string]struct{}{}
		for p, data := range insertions {
			for _, insertion := range data {
				points := pointsByLang[insertion.lang]
				if points == nil {
					points = map[string]struct{}{}
					pointsByLang[insertion.lang] = points
				}
				points[p] = struct{}{}
			}
		}
		var buf bytes.Buffer
		_, _ = fmt.Fprintf(&buf, "missing insertion point(s) in %q: ", fileName)
		first := true
		for lang, points := range pointsByLang {
			pointSlice := make([]string, 0, len(points))
			for p := range points {
				pointSlice = append(pointSlice, p)
			}
			if first {
				first = false
			} else {
				buf.WriteString("; ")
			}
			_, _ = fmt.Fprintf(&buf, "%q wants to insert into %s", lang, strings.Join(pointSlice, ","))
		}

		return nil, errors.New(buf.String())
	}

	result.Write(data)
	return &result, nil
}
