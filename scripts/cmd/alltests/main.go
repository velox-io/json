// Command alltests merges the repository's public-API test suites into a
// single synthetic test package, so the whole suite can run in ONE process
// with a randomized test order (go test -shuffle).
//
// Why: `make test` runs each package's tests in its own process, so every
// suite starts from pristine process-global state (type compiler caches,
// arena allocators, runtime caches). Merging the suites into one binary makes
// that state shared and the execution order random, which surfaces
// order-dependent and cross-module interference that per-package runs cannot
// observe.
//
// The generator copies each *_test.go file of the configured units verbatim
// except for two mechanical rewrites:
//
//   - the package clause becomes the synthetic package name
//   - top-level identifiers that collide across units are renamed to
//     <unit>_<name> in all but the first owning unit
//
// Renames are applied to identifier occurrences in expression, type and
// signature positions, which keeps them a consistent alpha-rename:
//
//   - selector names (x.name) are never renamed: they resolve inside the
//     operand's type, never to a package-level declaration
//   - composite-literal keys (K: v) are never renamed: they are field names
//   - struct field and interface method names are never renamed: they live in
//     their type's namespace. Function parameter, result, receiver and type
//     parameter names ARE renamed together with their uses
//
// A name reused in disjoint scopes (a local shadowing the package-level
// declaration) collapses into a single new name and fails compilation loudly
// instead of being silently mispatched. Colliding test function names are a
// hard error (a rename would hide the test from `go test` discovery); rename
// the source declaration instead.
//
// Data directories referenced by relative path from a unit directory are
// exposed in the output tree via symlinks: testdata/ inside the output
// directory, and tool-module testdata/ links (JSONTestSuite, benchmark) for
// the ../JSONTestSuite and ../benchmark references used by tests/compat and
// tests.
//
// Output lives under <tool module>/testdata/alltests, inside this module.
// The module's go.mod requires github.com/velox-io/json with a replace to
// the repository root, which is how the generated package resolves its
// imports of the library (including internal/valueabi; the internal
// visibility rule is import-path based, and this module lives under
// github.com/velox-io/json/). The go tool ignores testdata directories in
// ./... patterns, so the generated package never leaks into `go build ./...`
// or lint runs; it is only compiled by an explicit `go test
// ./testdata/alltests` run from the tool module (see `make test-shuffle`).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// This module's path must stay under the repository module path: the
	// generated package imports internal/valueabi, and internal visibility
	// is decided by import-path prefix.
	toolModule = "github.com/velox-io/json/scripts/cmd/alltests"
	repoModule = "github.com/velox-io/json"
)

type unit struct {
	dir  string // repo-relative unit directory, e.g. "tests/compat"
	flat string // file-name prefix derived from the base, e.g. "compat"
}

type sourceFile struct {
	dir  string // repo-relative
	name string // base name
	src  []byte
	fset *token.FileSet
	af   *ast.File
}

type edit struct {
	off, end int
	text     string
}

func main() {
	unitsFlag := flag.String("units", "tests,tests/compat,stream", "comma-separated repo-relative suite directories")
	outFlag := flag.String("out", "testdata/alltests", "repo-relative output directory")
	pkgFlag := flag.String("pkg", "alltests", "synthetic package name")
	flag.Parse()

	root, err := findRepoRoot()
	fatalIf(err)
	toolDir, err := findToolDir()
	fatalIf(err)

	var units []unit
	for _, s := range strings.Split(*unitsFlag, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		units = append(units, unit{dir: s, flat: filepath.Base(s)})
	}
	if len(units) == 0 {
		fatalIf(fmt.Errorf("no units given"))
	}

	// Load and parse every build-tag-eligible test file of every unit.
	files := make([][]*sourceFile, len(units))
	for i, u := range units {
		fs, err := loadUnit(filepath.Join(root, u.dir))
		if err != nil {
			fatalIf(fmt.Errorf("%s: %w", u.dir, err))
		}
		files[i] = fs
	}

	// Collect top-level declared names (methods excluded: they live in their
	// receiver type's namespace and never collide across packages).
	decls := make([]map[string]bool, len(units))
	for i, fs := range files {
		decls[i] = map[string]bool{}
		for _, f := range fs {
			for _, d := range f.af.Decls {
				for _, name := range declNames(d) {
					decls[i][name] = true
				}
			}
		}
	}

	// Resolve cross-unit name collisions: the first owner keeps the name,
	// later owners get a <unit>_<name> rename.
	renames := make([]map[string]string, len(units))
	for i := range renames {
		renames[i] = map[string]string{}
	}
	owners := map[string][]int{}
	for i, dm := range decls {
		for name := range dm {
			owners[name] = append(owners[name], i)
		}
	}
	for _, name := range sortedKeys(owners) {
		os := owners[name]
		if len(os) < 2 {
			continue
		}
		if strings.HasPrefix(name, "Test") {
			var where []string
			for _, i := range os {
				where = append(where, units[i].dir)
			}
			fatalIf(fmt.Errorf("test function %s declared in multiple units (%s): "+
				"rename it in the source suites, an automatic rename would hide it from go test",
				name, strings.Join(where, ", ")))
		}
		for _, i := range os[1:] {
			newName := units[i].flat + "_" + name
			renames[i][name] = newName
			for j, dm := range decls {
				if dm[newName] {
					fatalIf(fmt.Errorf("rename %s -> %s (unit %s) collides with a declaration in %s",
						name, newName, units[i].dir, units[j].dir))
				}
			}
		}
	}

	// Emit the merged package.
	out := filepath.Join(toolDir, *outFlag)
	fatalIf(os.RemoveAll(out))
	fatalIf(os.MkdirAll(out, 0o755))

	testCount := 0
	for i, fs := range files {
		for _, f := range fs {
			testCount += emit(f, units[i], renames[i], *pkgFlag, out)
		}
	}

	// Expose data directories referenced by relative path from the unit dirs.
	fatalIf(symlink(root, "tests/testdata", filepath.Join(out, "testdata")))
	// tests/compat and tests reach their data through ../JSONTestSuite and
	// ../benchmark, which from the output directory resolve into this
	// module's testdata/.
	fatalIf(symlink(root, "tests/JSONTestSuite", filepath.Join(toolDir, "testdata", "JSONTestSuite")))
	fatalIf(symlink(root, "benchmark", filepath.Join(toolDir, "testdata", "benchmark")))

	for i, u := range units {
		fmt.Printf("unit %-12s %2d files  (renames: %s)\n", u.dir, len(files[i]), renameSummary(renames[i]))
	}
	fmt.Printf("merged %d test functions into %s (package %s)\n", testCount, *outFlag, *pkgFlag)
}

// findToolDir locates this module's directory by walking up from the
// working directory to the go.mod declaring the tool module path. `go run .`
// is invoked from the module directory, so the search starts there.
func findToolDir() (string, error) {
	return findModuleRoot(toolModule)
}

// findRepoRoot locates the repository root by walking up from the tool
// directory to the go.mod declaring the repository module path. The match
// is on the exact module path, not a prefix: this module's own go.mod
// (github.com/velox-io/json/scripts/cmd/alltests) would otherwise match.
func findRepoRoot() (string, error) {
	toolDir, err := findToolDir()
	if err != nil {
		return "", err
	}
	return findModuleRootFrom(filepath.Dir(toolDir), repoModule)
}

// findModuleRoot walks up from the working directory looking for the go.mod
// that declares the given module path.
func findModuleRoot(module string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return findModuleRootFrom(cwd, module)
}

func findModuleRootFrom(start, module string) (string, error) {
	dir := start
	for {
		if mod := modulePath(dir); mod == module {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("module root (go.mod with %q) not found", module)
		}
		dir = parent
	}
}

// modulePath returns the module path declared by the go.mod in dir, or "".
func modulePath(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if mod, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			if i := strings.IndexByte(mod, ' '); i >= 0 {
				mod = mod[:i]
			}
			return strings.TrimSpace(mod)
		}
	}
	return ""
}

// loadUnit parses every *_test.go file of dir that the current toolchain and
// build tags would include, mirroring what `go test <dir>` compiles.
func loadUnit(dir string) ([]*sourceFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	ctxt := build.Default
	var out []*sourceFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		match, err := ctxt.MatchFile(dir, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !match {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, &sourceFile{dir: dir, name: name, src: src, fset: fset, af: af})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no test files matched")
	}
	return out, nil
}

// declNames returns the top-level names declared by a declaration, excluding
// method names and blank identifiers.
func declNames(d ast.Decl) []string {
	var names []string
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil && d.Name.Name != "_" {
			names = append(names, d.Name.Name)
		}
	case *ast.GenDecl:
		if d.Tok == token.IMPORT {
			return nil
		}
		for _, s := range d.Specs {
			switch s := s.(type) {
			case *ast.TypeSpec:
				if s.Name.Name != "_" {
					names = append(names, s.Name.Name)
				}
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if n.Name != "_" {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	return names
}

// emit rewrites one source file (package clause plus collision renames) into
// the merged package and returns the number of test functions it contains.
func emit(f *sourceFile, u unit, renames map[string]string, pkg, out string) int {
	edits := []edit{}

	// Package clause rewrite.
	pkgName := f.af.Name
	edits = append(edits, edit{
		off:  f.fset.Position(pkgName.Pos()).Offset,
		end:  f.fset.Position(pkgName.End()).Offset,
		text: pkg,
	})

	testCount := 0
	r := &renamer{f: f, renames: renames}
	for _, d := range f.af.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			if fd.Recv == nil && strings.HasPrefix(fd.Name.Name, "Test") {
				testCount++
			}
		}
		ast.Walk(r, d)
	}
	edits = append(edits, r.edits...)

	sort.Slice(edits, func(i, j int) bool { return edits[i].off < edits[j].off })
	var buf bytes.Buffer
	last := 0
	for i, e := range edits {
		if e.off < last || (i > 0 && e.off < edits[i-1].end) {
			fatalIf(fmt.Errorf("%s/%s: overlapping edits", u.dir, f.name))
		}
		buf.Write(f.src[last:e.off])
		buf.WriteString(e.text)
		last = e.end
	}
	buf.Write(f.src[last:])

	dst := filepath.Join(out, u.flat+"__"+f.name)
	fatalIf(os.WriteFile(dst, buf.Bytes(), 0o644))
	return testCount
}

// renamer records text edits for every identifier occurrence that matches a
// rename, with namespace-aware exceptions (see the package comment).
type renamer struct {
	f       *sourceFile
	renames map[string]string
	edits   []edit
}

func (r *renamer) rename(id *ast.Ident) {
	nn, ok := r.renames[id.Name]
	if !ok {
		return
	}
	r.edits = append(r.edits, edit{
		off:  r.f.fset.Position(id.Pos()).Offset,
		end:  r.f.fset.Position(id.End()).Offset,
		text: nn,
	})
}

// walkFields walks a field list. Signature fields (params, results, receivers,
// type parameters) declare value or type names whose uses are renamed too, so
// their names participate in the alpha-rename. Struct fields and interface
// methods are namespaces of their own and never renamed.
func (r *renamer) walkFields(fl *ast.FieldList, sigNames bool) {
	if fl == nil {
		return
	}
	for _, f := range fl.List {
		if sigNames {
			for _, id := range f.Names {
				r.rename(id)
			}
		}
		ast.Walk(r, f.Type)
	}
}

func (r *renamer) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.Ident:
		r.rename(n)
		return nil
	case *ast.SelectorExpr:
		// n.Sel resolves inside the type of n.X, never to a package-level
		// declaration of the merged unit.
		ast.Walk(r, n.X)
		return nil
	case *ast.KeyValueExpr:
		// A plain-ident key is a field name.
		if _, ok := n.Key.(*ast.Ident); ok {
			ast.Walk(r, n.Value)
			return nil
		}
	case *ast.FuncDecl:
		if n.Recv != nil {
			r.walkFields(n.Recv, true)
		} else {
			r.rename(n.Name)
		}
		ast.Walk(r, n.Type)
		if n.Body != nil {
			ast.Walk(r, n.Body)
		}
		return nil
	case *ast.FuncType:
		r.walkFields(n.TypeParams, true)
		r.walkFields(n.Params, true)
		r.walkFields(n.Results, true)
		return nil
	case *ast.StructType:
		r.walkFields(n.Fields, false)
		return nil
	case *ast.InterfaceType:
		r.walkFields(n.Methods, false)
		return nil
	}
	return r
}

// symlink makes dst a relative symlink to the repo-relative src. Every
// data source is a git-tracked directory: a missing source means the
// checkout is broken, and silently skipping the link would leave the
// generated tree failing confusingly at test time with bare open errors,
// so it is a hard error. Existing dst entries are replaced.
func symlink(root, src, dst string) error {
	srcAbs := filepath.Join(root, src)
	if _, err := os.Stat(srcAbs); err != nil {
		return fmt.Errorf("data source %s missing: %w", src, err)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	rel, err := filepath.Rel(filepath.Dir(dst), srcAbs)
	if err != nil {
		return err
	}
	return os.Symlink(rel, dst)
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func renameSummary(m map[string]string) string {
	if len(m) == 0 {
		return "none"
	}
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+" -> "+m[k])
	}
	return strings.Join(parts, ", ")
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "alltests:", err)
		os.Exit(1)
	}
}
