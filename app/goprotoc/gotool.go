package goprotoc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// goToolScheme is the prefix that marks a --plugin location as a Go package
// path, to be run via "go tool" using the tool directives in the consuming
// module's go.mod, instead of as a plain executable.
const goToolScheme = "gotool:"

// trimGoToolScheme removes the goToolScheme prefix from a plugin location,
// reporting whether it was present.
func trimGoToolScheme(location string) (string, bool) {
	pkg := strings.TrimPrefix(location, goToolScheme)
	return pkg, pkg != location
}

// goToolBaseName returns the default binary name of the given Go package path,
// which is its last element with any major version suffix removed. This matches
// how the "go tool" command names the tools declared in a module's go.mod.
func goToolBaseName(pkg string) string {
	base := path.Base(pkg)
	if isMajorVersionSuffix(base) {
		base = path.Base(path.Dir(pkg))
	}
	return base
}

// isMajorVersionSuffix returns true if the given package path element is a
// major version suffix, like "v2".
func isMajorVersionSuffix(elem string) bool {
	if len(elem) < 2 || elem[0] != 'v' {
		return false
	}
	for _, r := range elem[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// checkGoToolPackage validates that the given string can be used as the Go
// package path of a tool declared in the consuming module's go.mod.
func checkGoToolPackage(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("%s plugin location must be followed by a Go package path", goToolScheme)
	}
	if strings.Contains(pkg, "@") {
		return fmt.Errorf("%s plugin location %q must not include a version: the version comes from the go.mod of the module being generated", goToolScheme, pkg)
	}
	return nil
}

// goToolResolver locates the Go toolchain and the protoc plugins declared as
// tools in the go.mod of the module that contains the current directory. The
// lookups are performed at most once, lazily.
type goToolResolver struct {
	goOnce sync.Once
	goBin  string
	goErr  error

	toolsOnce sync.Once
	tools     *toolDirectives
	toolsErr  error
}

// toolDirectives are the tool directives declared in a module's go.mod.
type toolDirectives struct {
	// packages holds every declared package path.
	packages map[string]struct{}
	// plugins holds the declared packages that are named "protoc-gen-*", keyed
	// by the output language they generate (so a tool named "protoc-gen-go" is
	// keyed as "go").
	plugins map[string]string
}

// goBinary returns the path of the "go" command.
func (r *goToolResolver) goBinary() (string, error) {
	r.goOnce.Do(func() {
		p, err := exec.LookPath("go")
		if err == nil {
			r.goBin = p
			return
		}
		r.goErr = err
		// The go command is not on the PATH, but we may still have been run by
		// it, in which case GOROOT points at the toolchain to use.
		if goroot := os.Getenv("GOROOT"); goroot != "" {
			if p, err := exec.LookPath(filepath.Join(goroot, "bin", "go")); err == nil {
				r.goBin = p
				r.goErr = nil
			}
		}
	})
	return r.goBin, r.goErr
}

// toolDirectives returns the tool directives declared in the go.mod of the
// module that contains the current directory.
func (r *goToolResolver) toolDirectives() (*toolDirectives, error) {
	r.toolsOnce.Do(func() {
		goBin, err := r.goBinary()
		if err != nil {
			r.toolsErr = err
			return
		}
		// Errors here are expected when not inside a module, so the go command's
		// own diagnostics are folded into the returned error instead of being
		// printed.
		out, err := exec.Command(goBin, "mod", "edit", "-json").Output()
		if err != nil {
			r.toolsErr = fmt.Errorf("failed to read tool directives from go.mod: %v", goCmdError(err))
			return
		}
		r.tools, r.toolsErr = parseToolDirectives(out)
	})
	return r.tools, r.toolsErr
}

// declaresTool reports whether go.mod declares the given package as a tool. If
// the tool directives cannot be read at all, it reports true, so that an
// unrelated failure is not misattributed to a missing directive.
func (r *goToolResolver) declaresTool(pkg string) bool {
	tools, err := r.toolDirectives()
	if err != nil {
		return true
	}
	_, ok := tools.packages[pkg]
	return ok
}

// parseToolDirectives extracts the tool directives from the output of
// "go mod edit -json". Tools whose package is not named "protoc-gen-*" are
// still recorded, but only under packages: they cannot be referred to by a
// --<PLUGIN>_out flag, only by an explicit --plugin flag.
func parseToolDirectives(data []byte) (*toolDirectives, error) {
	// Only the tool directives are of interest; the rest of the go.mod is
	// intentionally not modeled.
	var mod struct {
		Tool []struct {
			Path string
		}
	}
	if err := json.Unmarshal(data, &mod); err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %v", err)
	}
	tools := &toolDirectives{
		packages: map[string]struct{}{},
		plugins:  map[string]string{},
	}
	for _, t := range mod.Tool {
		tools.packages[t.Path] = struct{}{}
		lang, ok := pluginLang(goToolBaseName(t.Path))
		if !ok {
			continue
		}
		if prev, ok := tools.plugins[lang]; ok {
			return nil, fmt.Errorf("go.mod declares more than one tool named %q: %s and %s; use --plugin=%s<package> to select one",
				pluginExeName(lang), prev, t.Path, goToolScheme)
		}
		tools.plugins[lang] = t.Path
	}
	return tools, nil
}

// goCmdError describes an error from running the go command, preferring the go
// command's own message over a bare exit status.
func goCmdError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return errors.New(msg)
		}
	}
	return err
}
