package config_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keskinhakanemre/go-core/config"
)

type serviceConfig struct {
	config.BaseConfig `mapstructure:",squash"`

	DB struct {
		URL      string        `mapstructure:"url"`
		Password config.Secret `mapstructure:"password"`
	} `mapstructure:"db"`

	Features []string `mapstructure:"features"`
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", `
app:
  name: svc
http:
  port: "8080"
  request_timeout: 3s
db:
  url: postgres://base
`)
	writeFile(t, dir, "config.prod.yaml", `
http:
  port: "9090"
db:
  url: postgres://prod
`)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("APP_DB_URL", "postgres://env")
	// Not present in any YAML file: must still be picked up.
	t.Setenv("APP_DB_PASSWORD", "s3cret")
	t.Setenv("APP_HTTP_IDLE_TIMEOUT", "42s")
	t.Setenv("APP_FEATURES", "a,b")

	cfg, err := config.Load[serviceConfig](config.Options{Paths: []string{dir}, EnvPrefix: "APP"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.HTTP.Port != "9090" {
		t.Errorf("env overlay not applied, port=%s", cfg.HTTP.Port)
	}
	if cfg.DB.URL != "postgres://env" {
		t.Errorf("env override not applied, url=%s", cfg.DB.URL)
	}
	if cfg.DB.Password.Value() != "s3cret" {
		t.Errorf("secret not loaded from env")
	}
	if cfg.HTTP.IdleTimeout != 42*time.Second {
		t.Errorf("idle timeout = %s", cfg.HTTP.IdleTimeout)
	}
	if cfg.HTTP.RequestTimeout != 3*time.Second {
		t.Errorf("request timeout = %s", cfg.HTTP.RequestTimeout)
	}
	if cfg.App.Env != "prod" {
		t.Errorf("app env = %s", cfg.App.Env)
	}
	if cfg.Log.Level != "info" || !cfg.Metrics.Enabled {
		t.Errorf("defaults not applied: %+v %+v", cfg.Log, cfg.Metrics)
	}
	if strings.Join(cfg.Features, "|") != "a|b" {
		t.Errorf("features = %v", cfg.Features)
	}
}

func TestLoadWithoutFiles(t *testing.T) {
	t.Setenv("APP_NAME", "from-env")
	cfg, err := config.Load[config.BaseConfig](config.Options{Paths: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Name != "from-env" || cfg.HTTP.Addr() != ":8080" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadValidation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "log:\n  level: loud\n")
	_, err := config.Load[config.BaseConfig](config.Options{Paths: []string{dir}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, want := range []string{"App.Name", "Log.Level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

type crossValidated struct {
	config.BaseConfig `mapstructure:",squash"`
}

func (c *crossValidated) Validate() error {
	if c.Tracing.Enabled && c.Tracing.Endpoint == "" {
		return errors.New("tracing.endpoint is required when tracing is enabled")
	}
	return nil
}

func TestLoadCustomValidator(t *testing.T) {
	t.Setenv("APP_NAME", "svc")
	t.Setenv("TRACING_ENABLED", "true")
	_, err := config.Load[crossValidated](config.Options{Paths: []string{t.TempDir()}})
	if err == nil || !strings.Contains(err.Error(), "tracing.endpoint") {
		t.Fatalf("expected custom validation error, got %v", err)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "app: [unclosed")
	if _, err := config.Load[config.BaseConfig](config.Options{Paths: []string{dir}}); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSecretIsMasked(t *testing.T) {
	s := config.Secret("hunter2")
	for _, out := range []string{
		fmt.Sprint(s), fmt.Sprintf("%v %s %+v %#v", s, s, s, s),
		fmt.Sprintf("%+v", struct{ P config.Secret }{s}),
	} {
		if strings.Contains(out, "hunter2") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	b, _ := json.Marshal(struct{ P config.Secret }{s})
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("secret leaked in json: %s", b)
	}
	if s.Value() != "hunter2" {
		t.Fatal("Value must return the plain secret")
	}
}
