// Command gocore scaffolds new services built on go-core.
//
//	go run github.com/keskinhakanemre/go-core/cmd/gocore@latest new github.com/acme/order-service
//
// Flags (after the module path):
//
//	-dir           target directory (default: last element of the module path)
//	-core-version  go-core version to require (default: the version of this tool, else latest)
//	-core-replace  local path to a go-core checkout; adds a replace directive
//	-no-tidy       skip `go get` / `go mod tidy`
//	-force         write into a non-empty directory
package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"text/template"
)

const (
	coreModule = "github.com/keskinhakanemre/go-core"
	goVersion  = "1.26"
	tmplRoot   = "templates/service"
)

//go:embed all:templates
var templates embed.FS

// Data is passed to every template. Templates use [[ ]] delimiters so that
// GitHub Actions / Grafana {{ }} expressions need no escaping.
type Data struct {
	Module     string // github.com/acme/order-service
	Name       string // order-service (DNS-1123 safe)
	CoreModule string
	GoVersion  string
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "gocore:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		usage(out)
		return errors.New("missing command")
	}
	switch args[0] {
	case "new":
		return cmdNew(ctx, args[1:], out)
	case "version":
		_, _ = fmt.Fprintln(out, toolVersion())
		return nil
	case "help", "-h", "--help":
		usage(out)
		return nil
	default:
		usage(out)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(out io.Writer) {
	_, _ = fmt.Fprint(out, `usage:
  gocore new <module-path> [-dir DIR] [-core-version VERSION] [-core-replace PATH] [-no-tidy] [-force]
  gocore version
`)
}

func cmdNew(ctx context.Context, args []string, out io.Writer) error {
	fset := flag.NewFlagSet("new", flag.ContinueOnError)
	fset.SetOutput(out)
	dir := fset.String("dir", "", "target directory (default: last element of the module path)")
	coreVersion := fset.String("core-version", "", "go-core version to require (default: this tool's version or latest)")
	coreReplace := fset.String("core-replace", "", "local go-core checkout to use via a replace directive")
	noTidy := fset.Bool("no-tidy", false, "skip go get / go mod tidy")
	force := fset.Bool("force", false, "allow writing into a non-empty directory")

	// Accept the module path before or after the flags.
	var module string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		module, args = args[0], args[1:]
	}
	if err := fset.Parse(args); err != nil {
		return err
	}
	if module == "" {
		module = fset.Arg(0)
	}
	if err := checkModulePath(module); err != nil {
		return err
	}

	data := Data{Module: module, Name: serviceName(module), CoreModule: coreModule, GoVersion: goVersion}
	if *dir == "" {
		*dir = path.Base(module)
	}
	if err := ensureEmpty(*dir, *force); err != nil {
		return err
	}
	if err := Generate(*dir, data); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "created %s in %s\n", module, *dir)

	if *coreReplace != "" {
		abs, err := filepath.Abs(*coreReplace)
		if err != nil {
			return err
		}
		if err := addReplace(filepath.Join(*dir, "go.mod"), abs); err != nil {
			return err
		}
	}

	if !*noTidy {
		if *coreReplace == "" {
			v := *coreVersion
			if v == "" {
				v = toolVersion()
			}
			if err := goCmd(ctx, *dir, out, "get", coreModule+"@"+v); err != nil {
				return err
			}
		}
		if err := goCmd(ctx, *dir, out, "mod", "tidy"); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(out, `
next steps:
  cd %s
  git init
  make run        # or: go run ./cmd/api
  make test
`, *dir)
	return nil
}

// Generate renders the service template into dir.
func Generate(dir string, data Data) error {
	root, err := fs.Sub(templates, tmplRoot)
	if err != nil {
		return err
	}
	return fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		content, err := render(p, raw, data)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(p, ".tmpl")))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // project directories
			return err
		}
		return os.WriteFile(target, content, 0o644) //nolint:gosec // generated source files are not secret
	})
}

func render(name string, raw []byte, data Data) ([]byte, error) {
	t, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	if strings.HasSuffix(name, ".go.tmpl") {
		src, err := format.Source(buf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", name, err)
		}
		return src, nil
	}
	return buf.Bytes(), nil
}

var (
	modulePathRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~\-/]*[A-Za-z0-9]$`)
	invalidName  = regexp.MustCompile(`[^a-z0-9-]+`)
)

func checkModulePath(m string) error {
	if m == "" {
		return errors.New("module path is required, e.g. gocore new github.com/acme/order-service")
	}
	if !modulePathRe.MatchString(m) || strings.Contains(m, "//") || strings.Contains(m, "..") {
		return fmt.Errorf("invalid module path %q", m)
	}
	return nil
}

// serviceName turns the last module path element into a DNS-1123 label
// usable for Kubernetes resources and Prometheus jobs.
func serviceName(module string) string {
	name := strings.ToLower(path.Base(module))
	name = invalidName.ReplaceAllString(strings.ReplaceAll(name, "_", "-"), "-")
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if name == "" {
		name = "service"
	}
	return name
}

func ensureEmpty(dir string, force bool) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("directory %s is not empty (use -force to overwrite)", dir)
	}
	return nil
}

func addReplace(goMod, target string) error {
	f, err := os.OpenFile(goMod, os.O_APPEND|os.O_WRONLY, 0) //nolint:gosec // path of the go.mod we just generated
	if err != nil {
		return err
	}
	target = filepath.ToSlash(target)
	if strings.ContainsAny(target, " \t") {
		target = `"` + target + `"`
	}
	_, err = fmt.Fprintf(f, "\nreplace %s => %s\n", coreModule, target)
	return errors.Join(err, f.Close())
}

func goCmd(ctx context.Context, dir string, out io.Writer, args ...string) error {
	_, _ = fmt.Fprintf(out, "go %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // fixed binary, args built by this tool
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// toolVersion is the go-core version this binary was built from when run
// via `go run .../cmd/gocore@vX.Y.Z`, otherwise "latest".
func toolVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "latest"
}
