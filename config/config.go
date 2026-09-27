// Package config loads service configuration from YAML files and environment
// variables into a caller supplied struct.
//
// Precedence (highest first):
//
//  1. environment variables (http.port -> HTTP_PORT, or APP_HTTP_PORT with EnvPrefix "APP")
//  2. config.<env>.yaml, where <env> is read from Options.EnvVar (default APP_ENV)
//  3. config.yaml
//  4. defaults registered by this package (see BaseConfig)
//
// Unlike plain viper, every field of the target struct can be overridden from the
// environment even when it does not appear in any YAML file, because the keys
// are derived from the struct's mapstructure tags.
//
// After loading, the struct is validated with go-playground/validator `validate`
// tags and, if the struct implements Validator, its Validate method.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

// BaseConfig holds the settings shared by every service. Embed it in the
// service config with `mapstructure:",squash"`.
type BaseConfig struct {
	App     AppConfig     `mapstructure:"app"`
	HTTP    HTTPConfig    `mapstructure:"http"`
	Log     LogConfig     `mapstructure:"log"`
	Tracing TracingConfig `mapstructure:"tracing"`
	Metrics MetricsConfig `mapstructure:"metrics"`
	Docs    DocsConfig    `mapstructure:"docs"`
}

type AppConfig struct {
	Name    string `mapstructure:"name" validate:"required"`
	Env     string `mapstructure:"env"` // local | dev | staging | prod
	Version string `mapstructure:"version"`
}

// IsProduction reports whether Env is "prod" or "production".
func (a AppConfig) IsProduction() bool { return a.Env == "prod" || a.Env == "production" }

type HTTPConfig struct {
	Host            string        `mapstructure:"host"`
	Port            string        `mapstructure:"port" validate:"required"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	RequestTimeout  time.Duration `mapstructure:"request_timeout"`  // per request context deadline, 0 disables
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"` // total budget for graceful shutdown
	DrainDelay      time.Duration `mapstructure:"drain_delay"`      // wait after readiness fails before closing listener
	BodyLimit       int           `mapstructure:"body_limit" validate:"gte=0"`
	AccessLog       bool          `mapstructure:"access_log"`
	CORSOrigins     []string      `mapstructure:"cors_origins"` // empty disables CORS middleware
}

// Addr returns host:port suitable for net.Listen.
func (h HTTPConfig) Addr() string { return net.JoinHostPort(h.Host, h.Port) }

type LogConfig struct {
	Level  string `mapstructure:"level" validate:"omitempty,oneof=debug info warn error dpanic panic fatal"`
	Format string `mapstructure:"format" validate:"omitempty,oneof=json console"`
}

type TracingConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	Endpoint    string  `mapstructure:"endpoint"` // OTLP/HTTP host:port, e.g. jaeger:4318
	Insecure    bool    `mapstructure:"insecure"`
	SampleRatio float64 `mapstructure:"sample_ratio" validate:"gte=0,lte=1"`
}

type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
}

// DocsConfig controls the OpenAPI document and Swagger UI endpoints.
// Disabled by default; enable it for local and staging environments.
type DocsConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Path     string `mapstructure:"path" validate:"omitempty,startswith=/"`      // Swagger UI
	SpecPath string `mapstructure:"spec_path" validate:"omitempty,startswith=/"` // OpenAPI JSON
}

// Validator can be implemented by config structs for cross field validation.
type Validator interface {
	Validate() error
}

type Options struct {
	FileName  string   // base file name without extension, default "config"
	Paths     []string // search paths, default ./config, ., /config
	EnvPrefix string   // e.g. "APP" -> APP_HTTP_PORT
	EnvVar    string   // variable selecting the environment overlay, default "APP_ENV"
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.env", "local")
	v.SetDefault("app.version", "dev")
	v.SetDefault("http.port", "8080")
	v.SetDefault("http.read_timeout", "10s")
	v.SetDefault("http.write_timeout", "10s")
	v.SetDefault("http.idle_timeout", "60s")
	v.SetDefault("http.request_timeout", "5s")
	v.SetDefault("http.shutdown_timeout", "15s")
	v.SetDefault("http.drain_delay", "0s")
	v.SetDefault("http.body_limit", 4*1024*1024)
	v.SetDefault("http.access_log", true)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("tracing.enabled", false)
	v.SetDefault("tracing.insecure", true)
	v.SetDefault("tracing.sample_ratio", 1.0)
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.path", "/metrics")
	v.SetDefault("docs.enabled", false)
	v.SetDefault("docs.path", "/docs")
	v.SetDefault("docs.spec_path", "/openapi.json")
}

// Load reads configuration files and environment variables into a new T.
func Load[T any](opts Options) (*T, error) {
	if opts.FileName == "" {
		opts.FileName = "config"
	}
	if len(opts.Paths) == 0 {
		opts.Paths = []string{"./config", ".", "/config"}
	}
	if opts.EnvVar == "" {
		opts.EnvVar = "APP_ENV"
	}

	v := viper.New()
	setDefaults(v)
	v.SetConfigType("yaml")
	for _, p := range opts.Paths {
		v.AddConfigPath(p)
	}

	v.SetConfigName(opts.FileName)
	if err := v.ReadInConfig(); err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("config: read %s: %w", opts.FileName, err)
	}

	env := os.Getenv(opts.EnvVar)
	if env != "" {
		v.SetConfigName(opts.FileName + "." + env)
		if err := v.MergeInConfig(); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("config: merge %s.%s: %w", opts.FileName, env, err)
		}
	}

	if opts.EnvPrefix != "" {
		v.SetEnvPrefix(opts.EnvPrefix)
	}
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var cfg T
	// viper only resolves env vars for keys it already knows; register every
	// key of T so that e.g. secrets need not be present in YAML at all.
	bindEnvs(v, reflect.TypeOf(cfg), "")

	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	// The environment selector wins over app.env from files.
	if env != "" {
		setAppEnv(&cfg, env)
	}

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate runs struct tag validation and the optional Validator interface.
func Validate(cfg any) error {
	if err := validate.Struct(cfg); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			msgs := make([]string, 0, len(verrs))
			for _, fe := range verrs {
				msgs = append(msgs, fmt.Sprintf("%s (%s)", fe.Namespace(), fe.Tag()))
			}
			return fmt.Errorf("config: invalid fields: %s", strings.Join(msgs, ", "))
		}
		return fmt.Errorf("config: validate: %w", err)
	}
	if c, ok := cfg.(Validator); ok {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	return nil
}

var validate = validator.New(validator.WithRequiredStructEnabled())

func isNotFound(err error) bool {
	var nf viper.ConfigFileNotFoundError
	return errors.As(err, &nf)
}

var timeType = reflect.TypeOf(time.Time{})

// bindEnvs walks t and registers every leaf key with viper.BindEnv.
func bindEnvs(v *viper.Viper, t reflect.Type, prefix string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, squash := parseTag(f)
		if name == "-" {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if squash {
			bindEnvs(v, ft, prefix)
			continue
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		if ft.Kind() == reflect.Struct && ft != timeType {
			bindEnvs(v, ft, key)
			continue
		}
		_ = v.BindEnv(key) // only fails without arguments
	}
}

func parseTag(f reflect.StructField) (name string, squash bool) {
	tag := f.Tag.Get("mapstructure")
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, p := range parts[1:] {
		if p == "squash" {
			squash = true
		}
	}
	if name == "" && !squash {
		name = strings.ToLower(f.Name)
	}
	return name, squash
}

// setAppEnv sets BaseConfig.App.Env if T embeds BaseConfig anywhere at the top level.
func setAppEnv(cfg any, env string) {
	rv := reflect.ValueOf(cfg).Elem()
	if rv.Kind() != reflect.Struct {
		return
	}
	if rv.Type() == reflect.TypeOf(BaseConfig{}) {
		rv.FieldByName("App").FieldByName("Env").SetString(env)
		return
	}
	for i := range rv.NumField() {
		f := rv.Field(i)
		if f.Type() == reflect.TypeOf(BaseConfig{}) && f.CanSet() {
			f.FieldByName("App").FieldByName("Env").SetString(env)
			return
		}
	}
}
