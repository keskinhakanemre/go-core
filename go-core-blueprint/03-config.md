# 03 — Config

## Kaynak projede

`pkg/config/config.go`: Viper ile `config.yaml` okunur, tek bir `AppConfig` struct'ına unmarshal edilir. Birden fazla arama yolu eklenmiş (`$PWD/config`, `.`, `/config`, `./config`).

**Eksikler:**
- Environment variable override yok → secret'lar (Couchbase şifresi) yaml içinde, git'e giriyor.
- Global `viper` instance kullanılıyor, test edilemez.
- Hata durumunda `panic`; ortam (dev/prod) ayrımı yok.
- Struct servise özel; tekrar kullanılamaz.

## Core tasarımı

- **Generic** `Load[T any]` — her servis kendi config struct'ını verir.
- Core, tüm servislerde ortak alanları içeren `BaseConfig` sunar; servis bunu `mapstructure:",squash"` ile embed eder.
- Öncelik sırası: **env var > `config.<env>.yaml` > `config.yaml`**.
- Env anahtarları: `http.port` → `HTTP_PORT` (opsiyonel prefix ile `APP_HTTP_PORT`).

## `config/config.go`

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// BaseConfig tüm servislerde ortak ayarlar. Servisler bunu squash ile embed eder.
type BaseConfig struct {
	App     AppConfig     `mapstructure:"app"`
	HTTP    HTTPConfig    `mapstructure:"http"`
	Log     LogConfig     `mapstructure:"log"`
	Tracing TracingConfig `mapstructure:"tracing"`
}

type AppConfig struct {
	Name    string `mapstructure:"name"`
	Env     string `mapstructure:"env"` // local | dev | staging | prod
	Version string `mapstructure:"version"`
}

type HTTPConfig struct {
	Port            string        `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	RequestTimeout  time.Duration `mapstructure:"request_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	BodyLimit       int           `mapstructure:"body_limit"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`  // debug | info | warn | error
	Format string `mapstructure:"format"` // json | console
}

type TracingConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	Endpoint    string  `mapstructure:"endpoint"` // host:port (OTLP HTTP, ör. jaeger:4318)
	Insecure    bool    `mapstructure:"insecure"`
	SampleRatio float64 `mapstructure:"sample_ratio"` // 0.0 - 1.0
}

type Options struct {
	FileName  string   // varsayılan "config"
	Paths     []string // varsayılan: ./config, ., /config
	EnvPrefix string   // ör. "APP" → APP_HTTP_PORT
	EnvVar    string   // ortamı belirleyen env, varsayılan "APP_ENV"
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.env", "local")
	v.SetDefault("http.port", "8080")
	v.SetDefault("http.read_timeout", "10s")
	v.SetDefault("http.write_timeout", "10s")
	v.SetDefault("http.idle_timeout", "60s")
	v.SetDefault("http.request_timeout", "5s")
	v.SetDefault("http.shutdown_timeout", "10s")
	v.SetDefault("http.body_limit", 4*1024*1024)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("tracing.enabled", false)
	v.SetDefault("tracing.insecure", true)
	v.SetDefault("tracing.sample_ratio", 1.0)
}

// Load config dosyalarını ve env değişkenlerini okuyup T'ye doldurur.
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

	// 1) Ana dosya
	v.SetConfigName(opts.FileName)
	if err := v.ReadInConfig(); err != nil {
		var nf viper.ConfigFileNotFoundError
		if !errors.As(err, &nf) {
			return nil, fmt.Errorf("config: read %s: %w", opts.FileName, err)
		}
	}

	// 2) Ortama özel override dosyası (config.prod.yaml vb.)
	if env := os.Getenv(opts.EnvVar); env != "" {
		v.SetConfigName(opts.FileName + "." + env)
		if err := v.MergeInConfig(); err != nil {
			var nf viper.ConfigFileNotFoundError
			if !errors.As(err, &nf) {
				return nil, fmt.Errorf("config: merge %s.%s: %w", opts.FileName, env, err)
			}
		}
	}

	// 3) Env değişkenleri
	if opts.EnvPrefix != "" {
		v.SetEnvPrefix(opts.EnvPrefix)
	}
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var cfg T
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &cfg, nil
}
```

> ⚠️ Viper'ın bilinen davranışı: `AutomaticEnv` + `Unmarshal` yalnızca Viper'ın **bildiği** anahtarlar için env'i okur (yaml'da veya `SetDefault`'ta geçen anahtarlar). Bu yüzden secret alanlarını yaml'da boş değerle (`password: ""`) tanımla ya da servis tarafında `v.SetDefault("couchbase.password", "")` ekle. Alternatif: `Options`'a `BindEnvs []string` ekleyip `v.BindEnv(key)` çağır.

## Servis tarafında kullanım

```go
// internal/config/config.go
package config

import coreconfig "github.com/hekanemre/go-core/config"

type Config struct {
	coreconfig.BaseConfig `mapstructure:",squash"`

	Couchbase struct {
		URL      string `mapstructure:"url"`
		Username string `mapstructure:"username"`
		Password string `mapstructure:"password"`
		Bucket   string `mapstructure:"bucket"`
	} `mapstructure:"couchbase"`

	Downstream struct {
		ExampleServiceURL string `mapstructure:"example_service_url"`
	} `mapstructure:"downstream"`
}

func Load() (*Config, error) {
	return coreconfig.Load[Config](coreconfig.Options{EnvPrefix: "APP"})
}
```

`config/config.yaml`:

```yaml
app:
  name: product-service
  env: local
  version: 0.1.0
http:
  port: "8080"
  request_timeout: 5s
log:
  level: debug
  format: console
tracing:
  enabled: true
  endpoint: localhost:4318
  sample_ratio: 1.0
couchbase:
  url: couchbase://localhost
  username: Administrator
  password: ""          # APP_COUCHBASE_PASSWORD ile ver
  bucket: products
downstream:
  example_service_url: http://localhost:8081
```

## Kurallar

- Secret'lar **asla** yaml'da gerçek değerle commit edilmez; env / K8s Secret ile gelir.
- Config'i log'larken secret alanlarını maskele (kaynak proje tüm config'i şifresiyle birlikte logluyor — bkz. [12](12-kaynak-projedeki-sorunlar.md)).
- Servis açılırken zorunlu alanları doğrula (`validation` paketi ile `validate:"required"` tag'leri kullanılabilir) ve eksikse hızlıca fail et.
