package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"

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

func doCodeGen(outputs map[string]string, fds []*desc.FileDescriptor, pluginDefs map[string]string) error {
	req := plugins.CodeGenRequest{
		Files:         fds,
		ProtocVersion: protocVersionStruct,
	}
	resps := map[string]*plugins.CodeGenResponse{}
	locations := map[string]string{}
	for lang, loc := range outputs {
		resp := plugins.NewCodeGenResponse(lang, nil)
		resps[lang] = resp
		locParts := strings.SplitN(loc, ":", 2)
		var arg string
		if len(locParts) > 1 {
			arg = locParts[0]
			locations[lang] = locParts[1]
		} else {
			locations[lang] = loc
		}
		pluginName := pluginDefs[lang]
		if err := executePlugin(&req, resp, pluginName, lang, arg); err != nil {
			return err
		}
	}
	results := map[string]fileOutput{}
	for lang, resp := range resps {
		err := resp.ForEach(func(name, insertionPoint string, data io.Reader) error {
			loc := locations[lang]
			fullName, err := filepath.Abs(filepath.Join(loc, name))
			if err != nil {
				return err
			}
			o := results[fullName]
			if insertionPoint == "" {
				if o.createdBy != "" {
					return fmt.Errorf("conflict: both %s and %s tried to create file %s", o.createdBy, lang, fullName)
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
			results[fullName] = o
			return nil
		})
		if err != nil {
			return err
		}
	}

	for fileName, output := range results {
		if output.contents == nil {
			return fmt.Errorf("%q generated invalid content for %s", output.createdBy, fileName)
		}
		fileContents := output.contents
		if len(output.insertions) > 0 {
			var err error
			fileContents, err = applyInsertions(fileName, output.contents, output.insertions)
			if err != nil {
				return err
			}
		}
		w, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, fileContents)
		if err != nil {
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

func executePlugin(req *plugins.CodeGenRequest, resp *plugins.CodeGenResponse, pluginName, lang, outputArg string) error {
	req.Args = strings.Split(outputArg, ",")
	if pluginName == "" {
		if _, ok := protocOutputs[lang]; ok {
			return driveProtocAsPlugin(req, resp, lang)
		}
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

	tmpDir, err := ioutil.TempDir("", "go-protoc")
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
	if err := os.Mkdir(outDir, 0777); err != nil {
		return err
	}

	fds := desc.ToFileDescriptorSet(req.Files...)
	descFile := filepath.Join(tmpDir, "descriptors")
	if fdsBytes, err := proto.Marshal(fds); err != nil {
		return err
	} else if err := ioutil.WriteFile(descFile, fdsBytes, 0666); err != nil {
		return err
	}

	args := make([]string, 2+len(req.Files)+len(req.Args))
	args[0] = "--descriptor_set_in=" + descFile
	args[1] = "--" + lang + "_out=" + outDir
	for i, arg := range req.Args {
		args[i+2] = arg
	}
	for i, f := range req.Files {
		args[i+2+len(req.Args)] = f.GetName()
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
		_, err = io.Copy(out, in)
		return err
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
		data, err = ioutil.ReadAll(contents)
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
				insData, err := ioutil.ReadAll(ins.data)
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
		var sb strings.Builder
		_, _ = fmt.Fprintf(&sb, "missing insertion point(s) in %q: ", fileName)
		first := true
		for lang, points := range pointsByLang {
			pointSlice := make([]string, 0, len(points))
			for p := range points {
				pointSlice = append(pointSlice, p)
			}
			if first {
				first = false
			} else {
				sb.WriteString("; ")
			}
			_, _ = fmt.Fprintf(&sb, "%q wants to insert into %s", lang, strings.Join(pointSlice, ","))
		}

		return nil, errors.New(sb.String())
	}

	result.Write(data)
	return &result, nil
}
