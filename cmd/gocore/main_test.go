package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceName(t *testing.T) {
	cases := map[string]string{
		"github.com/acme/order-service": "order-service",
		"github.com/acme/Order_Service": "order-service",
		"example.com/x/v2.api":          "v2-api",
		"svc":                           "svc",
	}
	for in, want := range cases {
		if got := serviceName(in); got != want {
			t.Errorf("serviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckModulePath(t *testing.T) {
	for _, bad := range []string{"", "has space/x", "a//b", "../x", "x/"} {
		if checkModulePath(bad) == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
	if err := checkModulePath("github.com/acme/order-service"); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateRendersEverything(t *testing.T) {
	dir := t.TempDir()
	data := Data{Module: "github.com/acme/order-service", Name: "order-service", CoreModule: coreModule, GoVersion: goVersion}
	if err := Generate(dir, data); err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{"go.mod", "cmd/api/main.go", "Dockerfile", ".gitignore", ".github/workflows/ci.yml", "CLAUDE.md", "deploy/k8s/app.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(p, ".tmpl") {
			t.Errorf("template suffix not stripped: %s", p)
		}
		b, _ := os.ReadFile(p)
		if bytes.Contains(b, []byte("[[")) || bytes.Contains(b, []byte("<no value>")) {
			t.Errorf("unrendered placeholder in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ci, _ := os.ReadFile(filepath.Join(dir, ".github/workflows/ci.yml"))
	if !bytes.Contains(ci, []byte("${{ github.sha }}")) {
		t.Error("GitHub Actions expressions must be preserved")
	}
}

func TestNewRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run(context.Background(), []string{"new", "github.com/acme/svc", "-dir", dir, "-no-tidy"}, &out)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected non-empty error, got %v", err)
	}
}

// TestGeneratedServiceBuildsAndPasses generates a service wired to this
// checkout of go-core and runs vet + tests in it. It needs the go toolchain
// and module downloads, so it is skipped with -short.
func TestGeneratedServiceBuildsAndPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end scaffold test in -short mode")
	}
	coreRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "order-service")

	var out bytes.Buffer
	if err := run(context.Background(), []string{"new", "github.com/acme/order-service", "-dir", dir, "-core-replace", coreRoot}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}, {"test", "./..."}} {
		cmd := exec.CommandContext(context.Background(), "go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, b)
		}
	}

	cmd := exec.CommandContext(context.Background(), "gofmt", "-l", ".")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err == nil && len(bytes.TrimSpace(b)) > 0 {
		t.Fatalf("generated files are not gofmt'ed:\n%s", b)
	}
}
