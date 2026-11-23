package supabase_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type complianceDoc struct {
	SDK struct {
		Name              string `yaml:"name"`
		TotalCapabilities int    `yaml:"total_capabilities"`
	} `yaml:"sdk"`
	Features map[string]struct {
		Status  string   `yaml:"status"`
		Note    string   `yaml:"note"`
		Symbols []string `yaml:"symbols"`
	} `yaml:"features"`
}

func collectExportedSymbols(t *testing.T, pkgRel string) map[string]bool {
	t.Helper()
	syms := make(map[string]bool)
	entries, err := os.ReadDir(pkgRel)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", pkgRel, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(pkgRel, e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", e.Name(), err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					syms[d.Name.Name] = true
				} else if len(d.Recv.List) > 0 {
					recvType := receiverTypeName(d.Recv.List[0].Type)
					if recvType != "" {
						syms[recvType+"."+d.Name.Name] = true
					}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						if sp.Name.IsExported() {
							syms[sp.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, name := range sp.Names {
							if name.IsExported() {
								syms[name.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return syms
}

func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return receiverTypeName(e.X)
	default:
		return ""
	}
}

func TestSDKComplianceMatrixAndExportedSymbols(t *testing.T) {
	raw, err := os.ReadFile("sdk-compliance.yaml")
	if err != nil {
		t.Fatalf("read sdk-compliance.yaml: %v", err)
	}
	var doc complianceDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse sdk-compliance.yaml: %v", err)
	}
	if len(doc.Features) != 257 {
		t.Fatalf("expected 257 canonical capabilities, got %d", len(doc.Features))
	}

	// If upstream supabase-js/sdk-compliance.yaml is present next to this repo, verify exact key parity.
	if upstreamRaw, err := os.ReadFile("../supabase-js/sdk-compliance.yaml"); err == nil {
		var up complianceDoc
		if err := yaml.Unmarshal(upstreamRaw, &up); err == nil {
			for k := range up.Features {
				if _, ok := doc.Features[k]; !ok {
					t.Errorf("missing upstream capability key %q in sdk-compliance.yaml", k)
				}
			}
		}
	}

	pkgCache := make(map[string]map[string]bool)
	notImplCount := 0
	for fid, entry := range doc.Features {
		if entry.Status == "not_implemented" {
			notImplCount++
			continue
		}
		if entry.Status != "implemented" {
			t.Errorf("feature %q has unexpected status %q", fid, entry.Status)
			continue
		}
		if len(entry.Symbols) == 0 {
			t.Errorf("implemented feature %q has no mapped Go symbols", fid)
			continue
		}
		for _, sym := range entry.Symbols {
			parts := strings.SplitN(sym, ".", 2)
			if len(parts) != 2 {
				t.Errorf("feature %q has malformed symbol %q", fid, sym)
				continue
			}
			pkg, name := parts[0], parts[1]
			syms, ok := pkgCache[pkg]
			if !ok {
				syms = collectExportedSymbols(t, pkg)
				pkgCache[pkg] = syms
			}
			if !syms[name] {
				t.Errorf("feature %q references non-exported or missing symbol %q in package %q", fid, name, pkg)
			}
		}
	}
	if notImplCount != 4 {
		t.Fatalf("expected 4 upstream not_implemented capabilities, got %d", notImplCount)
	}
}

func TestCoreSDKZeroGoravelFrameworkCoupling(t *testing.T) {
	corePkgs := []string{
		"./client",
		"./auth",
		"./postgrest",
		"./storage",
		"./realtime",
		"./functions",
		"./internal/transport",
	}
	args := append([]string{"list", "-f", "{{.ImportPath}}: {{range .Deps}}{{.}} {{end}}"}, corePkgs...)
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, string(out))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(line, "github.com/goravel/framework") {
			t.Fatalf("core SDK package illegally imports github.com/goravel/framework:\n%s", line)
		}
	}
}
