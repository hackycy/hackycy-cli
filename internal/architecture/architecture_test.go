package architecture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/hackycy/hackycy-cli"

var approvedPackageInventory = []string{
	"cmd/ycy",
	"internal/appconfig",
	"internal/architecture",
	"internal/filesession",
	"internal/fsthumbnail",
	"internal/gitprocess",
	"internal/logging",
	"internal/processprobe",
	"internal/sevenzipmanifest",
	"internal/sevenzipruntime",
	"internal/terminal",
	"internal/terminaltest",
	"internal/tunnelruntime",
	"internal/updater",
	"internal/windowsacl",
	"internal/ycycmd",
	"pkg/cmd/config",
	"pkg/cmd/config/cm",
	"pkg/cmd/config/cm/add",
	"pkg/cmd/config/cm/list",
	"pkg/cmd/config/cm/remove",
	"pkg/cmd/config/cm/set",
	"pkg/cmd/config/cm/test",
	"pkg/cmd/config/cm/use",
	"pkg/cmd/config/fork",
	"pkg/cmd/config/fork/add",
	"pkg/cmd/config/fork/list",
	"pkg/cmd/config/fork/remove",
	"pkg/cmd/diff",
	"pkg/cmd/export",
	"pkg/cmd/export/env",
	"pkg/cmd/factory",
	"pkg/cmd/fs",
	"pkg/cmd/git",
	"pkg/cmd/git/cm",
	"pkg/cmd/git/fork",
	"pkg/cmd/git/heat",
	"pkg/cmd/git/pulse",
	"pkg/cmd/rm",
	"pkg/cmd/root",
	"pkg/cmd/run",
	"pkg/cmd/tunnel",
	"pkg/cmd/tunnel/connect",
	"pkg/cmd/tunnel/server",
	"pkg/cmd/upgrade",
	"pkg/cmd/zip",
	"pkg/cmdutil",
	"tools/check-no-bun",
	"tools/hookctl",
	"tools/prepare-sevenzip",
	"tools/release-artifacts",
	"tools/web-browser-harness",
	"web",
}

var approvedAcceptancePackages = []string{
	"acceptance",
	"acceptance/web",
}

func TestActiveArchitecture(t *testing.T) {
	root := repositoryRoot(t)
	assertThinBinaryEntry(t, root)
	assertNoRootHandlerTransition(t, root)
	assertFinalPaths(t, root)
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			switch relative {
			case ".git", "legacy", ".scratch", "mock", "node_modules", "web/node_modules", "web/dist", "tools", "internal/terminal/prototype-vivid":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		violations = append(violations, inspectGoFile(root, path)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk active Go architecture: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("active architecture violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestApprovedPackageInventory(t *testing.T) {
	root := repositoryRoot(t)
	assertGoPackageInventory(t, root, "", approvedPackageInventory)
	wantAcceptance := append(append([]string(nil), approvedPackageInventory...), approvedAcceptancePackages...)
	assertGoPackageInventory(t, root, "acceptance", wantAcceptance)
}

func TestApprovedCharmV2ModuleGraph(t *testing.T) {
	root := repositoryRoot(t)
	output, stderr, err := runPinnedGoCommand(root, "list", "-m", "-json", "all")
	if err != nil {
		t.Fatalf("go list -m -json all: %v\n%s", err, stderr)
	}

	approved := map[string]string{
		"charm.land/bubbles/v2":   "v2.2.1",
		"charm.land/bubbletea/v2": "v2.0.9",
		"charm.land/huh/v2":       "v2.0.3",
		"charm.land/lipgloss/v2":  "v2.0.6",
		"charm.land/log/v2":       "v2.0.0",
	}
	found := make(map[string]string, len(approved))
	var violations []string
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var module moduleDescription
		err := decoder.Decode(&module)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode go list -m output: %v", err)
		}
		if want, ok := approved[module.Path]; ok {
			found[module.Path] = module.Version
			if module.Version != want {
				violations = append(violations, module.Path+" version "+module.Version+", want "+want)
			}
			continue
		}
		if legacyCharmModule(module.Path) {
			violations = append(violations, "unapproved Charm module "+module.Path+"@"+module.Version)
		}
	}
	for path, version := range approved {
		if found[path] == "" {
			violations = append(violations, "missing approved Charm module "+path+"@"+version)
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Charm v2 module graph violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestCommandPackagesDoNotImportCharmOrLog(t *testing.T) {
	root := repositoryRoot(t)
	commandRoot := filepath.Join(root, "pkg", "cmd")
	var violations []string
	err := filepath.WalkDir(commandRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			pathValue, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", relative, err)
			}
			if directCharmOrLogImport(pathValue) {
				violations = append(violations, filepath.ToSlash(relative)+": command package imports "+pathValue)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect command package imports: %v", err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("command package Charm/Log import violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestLogV2RemainsPrivateToLogging(t *testing.T) {
	root := repositoryRoot(t)
	var violations []string
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			switch relative {
			case ".git", ".scratch", "legacy", "mock", "node_modules", "web/node_modules", "web/dist", "tools", "internal/terminal/prototype-vivid":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			pathValue, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", relative, err)
			}
			if pathValue != "charm.land/log/v2" {
				continue
			}
			found = true
			if !strings.HasPrefix(relative, "internal/logging/") {
				violations = append(violations, relative+": imports private Log v2 adapter")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect Log v2 imports: %v", err)
	}
	if !found {
		t.Fatal("Log v2 is not imported by internal/logging")
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Log v2 import boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestApprovedDependencyDirection(t *testing.T) {
	root := repositoryRoot(t)
	for _, tags := range []string{"", "acceptance"} {
		graph, err := loadPackageGraph(root, tags)
		if err != nil {
			t.Fatalf("load package graph (tags=%q): %v", tags, err)
		}
		violations := auditPackageGraph(graph)
		if len(violations) > 0 {
			t.Fatalf("package dependency violations (tags=%q):\n%s", tags, strings.Join(violations, "\n"))
		}
	}
}

func assertFinalPaths(t *testing.T, root string) {
	t.Helper()
	for _, relative := range []string{"internal/cliapp", "internal/commands"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative))); err == nil {
			t.Fatalf("obsolete package path is present: %s", relative)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect obsolete package path %s: %v", relative, err)
		}
	}
	rootDirectory := filepath.Join(root, "pkg", "cmd", "root")
	for _, name := range []string{
		"config.go", "configcm.go", "configfork.go", "exportenv.go",
		"gitcm.go", "gitfork.go", "githeat.go", "gitpulse.go", "tunnel.go",
		"diff.go", "fs.go", "rm.go", "run.go", "upgrade.go", "zip.go",
	} {
		if _, err := os.Lstat(filepath.Join(rootDirectory, name)); err == nil {
			t.Fatalf("obsolete flat root command file is present: pkg/cmd/root/%s", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect flat root command file %s: %v", name, err)
		}
	}
}

type packageGraph struct {
	production map[string]map[string]bool
	tests      map[string]map[string]bool
}

type packageDescription struct {
	ImportPath   string
	ForTest      string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

type moduleDescription struct {
	Path    string
	Version string
}

func legacyCharmModule(path string) bool {
	return path == "github.com/charmbracelet/bubbles" ||
		path == "github.com/charmbracelet/bubbletea" ||
		path == "github.com/charmbracelet/huh" ||
		path == "github.com/charmbracelet/lipgloss" ||
		path == "github.com/charmbracelet/log" ||
		strings.HasPrefix(path, "charm.land/bubbles/") ||
		strings.HasPrefix(path, "charm.land/bubbletea/") ||
		strings.HasPrefix(path, "charm.land/huh/") ||
		strings.HasPrefix(path, "charm.land/lipgloss/") ||
		strings.HasPrefix(path, "charm.land/log/")
}

func directCharmOrLogImport(path string) bool {
	return strings.HasPrefix(path, "charm.land/") || strings.HasPrefix(path, "github.com/charmbracelet/")
}

func assertGoPackageInventory(t *testing.T, root, tags string, want []string) {
	t.Helper()
	args := []string{"list"}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	args = append(args, "./...")
	output, stderr, err := runPinnedGoCommand(root, args...)
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr)
	}
	got := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, modulePath+"/") {
			t.Fatalf("go %s returned non-module package %q", strings.Join(args, " "), line)
		}
		got = append(got, strings.TrimPrefix(line, modulePath+"/"))
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !sameStrings(got, want) {
		t.Fatalf("go %s package inventory = %v, want %v", strings.Join(args, " "), got, want)
	}
}

func loadPackageGraph(root, tags string) (packageGraph, error) {
	args := []string{"list"}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	args = append(args, "-json", "-deps", "-test", "./...")
	output, stderr, err := runPinnedGoCommand(root, args...)
	if err != nil {
		return packageGraph{}, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr)
	}
	graph := packageGraph{
		production: make(map[string]map[string]bool),
		tests:      make(map[string]map[string]bool),
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var description packageDescription
		err := decoder.Decode(&description)
		if err == io.EOF {
			break
		}
		if err != nil {
			return packageGraph{}, fmt.Errorf("decode go list output: %w", err)
		}
		source, ok := relativeModulePackage(description.ImportPath)
		if !ok || description.ForTest != "" || strings.HasSuffix(source, ".test") {
			continue
		}
		for _, imported := range description.Imports {
			if target, ok := relativeModulePackage(imported); ok {
				addGraphEdge(graph.production, source, target)
			}
		}
		for _, imported := range append(append([]string(nil), description.TestImports...), description.XTestImports...) {
			if target, ok := relativeModulePackage(imported); ok {
				addGraphEdge(graph.tests, source, target)
			}
		}
	}
	return graph, nil
}

func addGraphEdge(graph map[string]map[string]bool, source, target string) {
	if graph[source] == nil {
		graph[source] = make(map[string]bool)
	}
	graph[source][target] = true
}

func auditPackageGraph(graph packageGraph) []string {
	violations := make([]string, 0)
	for source, targets := range graph.production {
		for target := range targets {
			violations = append(violations, auditDependencyEdge(source, target, false)...)
		}
	}
	for source, targets := range graph.tests {
		for target := range targets {
			violations = append(violations, auditDependencyEdge(source, target, true)...)
		}
	}
	sort.Strings(violations)
	return violations
}

func auditDependencyEdge(source, imported string, testOnly bool) []string {
	if obsoletePackage(imported) {
		return []string{source + " imports obsolete package " + imported}
	}
	if testOnly {
		return nil
	}
	if source == "cmd/ycy" && imported != "internal/ycycmd" {
		return []string{source + " imports outside the thin entry boundary: " + imported}
	}
	if source == "internal/ycycmd" && !allowedYcycmdImport(imported) {
		return []string{source + " imports outside the composition boundary: " + imported}
	}
	if isInternalPackage(source) && source != "internal/ycycmd" && strings.HasPrefix(imported, "pkg/cmd") {
		return []string{source + " imports command package " + imported}
	}
	if source == "pkg/cmdutil" && strings.HasPrefix(imported, "pkg/cmd") {
		return []string{source + " imports command package " + imported}
	}
	if source == "pkg/cmd/factory" && strings.HasPrefix(imported, "pkg/cmd/") && imported != "pkg/cmdutil" {
		return []string{source + " imports command package " + imported}
	}
	if source == "pkg/cmd/root" && strings.HasPrefix(imported, "pkg/cmd/") && !isTopLevelCommandPackage(imported) {
		return []string{source + " imports non-top-level command package " + imported}
	}
	if strings.HasPrefix(source, "pkg/cmd/") {
		return validateTargetCommandImport(source, source, imported)
	}
	if strings.HasPrefix(source, "tools/") && strings.HasPrefix(imported, "pkg/cmd") {
		return []string{source + " imports command package " + imported}
	}
	return nil
}

func obsoletePackage(path string) bool {
	return path == "internal/cliapp" || path == "internal/commands" ||
		strings.HasPrefix(path, "internal/cliapp/") || strings.HasPrefix(path, "internal/commands/")
}

func relativeModulePackage(path string) (string, bool) {
	if path == modulePath {
		return "", true
	}
	prefix := modulePath + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	return strings.TrimPrefix(path, prefix), true
}

func runPinnedGoCommand(root string, args ...string) ([]byte, []byte, error) {
	command := exec.Command("go", args...)
	command.Dir = root
	command.Env = pinnedGoEnvironment()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.Output()
	return stdout, stderr.Bytes(), err
}

func pinnedGoEnvironment() []string {
	want := map[string]string{
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
		"CGO_ENABLED": "0",
	}
	environment := make([]string, 0, len(os.Environ())+len(want))
	for _, value := range os.Environ() {
		key, _, ok := strings.Cut(value, "=")
		if ok {
			if _, replace := want[key]; replace {
				continue
			}
		}
		environment = append(environment, value)
	}
	for key, value := range want {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func assertNoRootHandlerTransition(t *testing.T, root string) {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filepath.Join(root, "pkg", "cmd", "root", "app.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse root app: %v", err)
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "registerUpgrade" {
			t.Fatal("root retains Upgrade registration transition")
		}
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if typeSpec.Name.Name == "Dependencies" || typeSpec.Name.Name == "UpgradeHandler" {
				t.Fatalf("root retains transitional type %q", typeSpec.Name.Name)
			}
			if typeSpec.Name.Name != "App" {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if name.Name == "upgrade" {
						t.Fatal("root App retains Upgrade handler field")
					}
				}
			}
		}
	}
}

func assertThinBinaryEntry(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "cmd", "ycy")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read cmd/ycy: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "main.go" || entries[0].IsDir() {
		t.Fatalf("cmd/ycy inventory = %#v, want only main.go", entries)
	}

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filepath.Join(directory, "main.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse thin binary entry: %v", err)
	}
	for _, imported := range file.Imports {
		value, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil || (value != "os" && value != modulePath+"/internal/ycycmd") {
			t.Fatalf("thin binary import = %q, want os and internal/ycycmd only", value)
		}
	}
	var mainFunction *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "main" {
			mainFunction = function
			break
		}
	}
	if mainFunction == nil || mainFunction.Body == nil || len(mainFunction.Body.List) != 1 {
		t.Fatal("thin binary entry must contain one main statement")
	}
	expression, ok := mainFunction.Body.List[0].(*ast.ExprStmt)
	if !ok {
		t.Fatal("thin binary entry main statement is not a call")
	}
	exitCall, ok := expression.X.(*ast.CallExpr)
	if !ok || !isSelectorCall(exitCall, "os", "Exit") || len(exitCall.Args) != 1 {
		t.Fatal("thin binary entry must call os.Exit once")
	}
	mainCall, ok := exitCall.Args[0].(*ast.CallExpr)
	if !ok || !isSelectorCall(mainCall, "ycycmd", "Main") || len(mainCall.Args) != 1 {
		t.Fatal("thin binary entry must pass version to ycycmd.Main")
	}
	argument, ok := mainCall.Args[0].(*ast.Ident)
	if !ok || argument.Name != "version" {
		t.Fatal("thin binary entry must pass the injected version")
	}
}

func isSelectorCall(call *ast.CallExpr, packageName, functionName string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != functionName {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == packageName
}

func isTopLevelCommandPackage(path string) bool {
	parts := strings.Split(path, "/")
	return len(parts) == 3 && parts[0] == "pkg" && parts[1] == "cmd"
}

func inspectGoFile(root, path string) []string {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return []string{path + ": parse error: " + err.Error()}
	}
	relativeDir, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return []string{path + ": resolve package path: " + err.Error()}
	}
	relativeDir = filepath.ToSlash(relativeDir)
	var violations []string
	for _, segment := range strings.Split(relativeDir, "/") {
		switch segment {
		case "utils", "services", "interfaces", "adapters", "common":
			violations = append(violations, path+": forbidden generic package segment "+segment)
		}
	}
	for _, imported := range file.Imports {
		pathValue, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			violations = append(violations, path+": invalid import literal")
			continue
		}
		violations = append(violations, validateImport(relativeDir, path, pathValue)...)
	}
	violations = append(violations, inspectCalls(relativeDir, path, file)...)
	return violations
}

func validateImport(source, filePath, imported string) []string {
	var violations []string
	if imported == "github.com/spf13/cobra" || imported == "github.com/spf13/pflag" {
		if !cobraOwner(source) {
			violations = append(violations, filePath+": Cobra/pflag may be imported only by command packages")
		}
	}
	if strings.Contains(imported, "/legacy/"+"b"+"un") {
		violations = append(violations, filePath+": active code imports frozen legacy")
	}
	if strings.HasPrefix(imported, modulePath+"/") {
		violations = append(violations, validateInternalImport(source, filePath, strings.TrimPrefix(imported, modulePath+"/"))...)
	}
	return violations
}

func validateInternalImport(source, filePath, imported string) []string {
	var violations []string
	if strings.HasPrefix(imported, "internal/commands/") {
		violations = append(violations, source+": old command package import is forbidden: "+imported)
	}

	if isInternalPackage(source) && strings.HasPrefix(imported, "pkg/cmd") {
		if source != "internal/ycycmd" {
			violations = append(violations, filePath+": internal package imports command package "+imported)
		}
	}
	if source == "pkg/cmdutil" && strings.HasPrefix(imported, "pkg/cmd") {
		violations = append(violations, filePath+": pkg/cmdutil must not import "+imported)
	}
	if source == "pkg/cmd/factory" && strings.HasPrefix(imported, "pkg/cmd/") && imported != "pkg/cmdutil" {
		violations = append(violations, filePath+": command factory imports command package "+imported)
	}
	if strings.HasPrefix(source, "pkg/cmd/") {
		violations = append(violations, validateTargetCommandImport(source, filePath, imported)...)
	}
	if source == "cmd/ycy" && !allowedCurrentBinaryImport(imported) {
		violations = append(violations, filePath+": cmd/ycy imports outside its transition boundary: "+imported)
	}
	if source == "internal/ycycmd" && !allowedYcycmdImport(imported) {
		violations = append(violations, filePath+": internal/ycycmd imports outside composition boundary: "+imported)
	}
	if isInternalPackage(source) && imported == "cmd/ycy" {
		violations = append(violations, filePath+": internal package imports cmd/ycy")
	}
	if isInternalPackage(source) && imported == "legacy/bun" {
		violations = append(violations, filePath+": internal package imports legacy/bun")
	}
	if imported == "web" && !allowedWebassetsConsumer(source) {
		violations = append(violations, filePath+": webassets consumer is not an owning command or composition root")
	}
	return violations
}

func inspectCalls(relativeDir, path string, file *ast.File) []string {
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING && relativeDir != "internal/appconfig" {
			value, err := strconv.Unquote(literal.Value)
			if err == nil && (value == "config.json" || value == ".config.lock") {
				violations = append(violations, path+": config persistence is owned only by internal/appconfig")
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Exit" {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && identifier.Name == "os" && relativeDir != "cmd/ycy" {
			violations = append(violations, path+": os.Exit is owned only by cmd/ycy")
		}
		return true
	})
	return violations
}

func validateTargetCommandImport(source, filePath, imported string) []string {
	if !strings.HasPrefix(imported, "pkg/cmd/") {
		return nil
	}
	// A parent may import only its direct child groups/leaves. A leaf may not
	// import any sibling or parent; shared behavior belongs under internal.
	if source == "pkg/cmd/root" {
		return nil
	}
	if source == "pkg/cmd/factory" {
		return []string{filePath + ": factory imports command package " + imported}
	}
	if targetCommandParent(source, imported) {
		return nil
	}
	return []string{filePath + ": command package imports sibling or parent " + imported}
}

func targetCommandParent(source, imported string) bool {
	sourceParts := strings.Split(source, "/")
	importedParts := strings.Split(imported, "/")
	if len(importedParts) <= len(sourceParts) {
		return false
	}
	for index := range sourceParts {
		if sourceParts[index] != importedParts[index] {
			return false
		}
	}
	// A command parent may register only its direct child package. Leaves have
	// no children in the command package graph, so they cannot cross owners.
	return len(importedParts) == len(sourceParts)+1
}

func cobraOwner(path string) bool {
	return path == "pkg/cmd/root" || strings.HasPrefix(path, "pkg/cmd/")
}

func isInternalPackage(path string) bool {
	return strings.HasPrefix(path, "internal/")
}

func allowedCurrentBinaryImport(imported string) bool {
	return imported == "internal/ycycmd"
}

func allowedYcycmdImport(imported string) bool {
	return imported == "pkg/cmd/root" || imported == "pkg/cmd/factory" || imported == "pkg/cmd/upgrade" || imported == "pkg/cmdutil" || strings.HasPrefix(imported, "internal/") || imported == "web"
}

func allowedWebassetsConsumer(path string) bool {
	return path == "internal/ycycmd" || path == "pkg/cmd/diff" || path == "pkg/cmd/fs" || path == "pkg/cmd/tunnel/server" || path == "tools/web-browser-harness"
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine architecture test location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
