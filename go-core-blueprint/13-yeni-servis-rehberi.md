# 13 — Core ile Yeni Servis Açma ve Endpoint Ekleme

## A. Yeni servis iskeleti

```
<servis-adi>/
├── cmd/
│   └── api/
│       └── main.go                   # composition root (bkz. 10)
├── internal/
│   ├── config/
│   │   └── config.go                 # core BaseConfig + servise özel alanlar (bkz. 03)
│   ├── domain/
│   │   ├── product.go                # entity'ler
│   │   └── errors.go                 # domain sentinel hataları (apperror)
│   ├── app/
│   │   └── product/
│   │       ├── ports.go              # Repository, StockProvider vb. interface'ler
│   │       ├── create_product.go     # Request/Response + Handler
│   │       ├── create_product_test.go
│   │       ├── get_product.go
│   │       └── get_product_test.go
│   ├── infra/
│   │   ├── couchbase/                # repository implementasyonları
│   │   │   └── product_repository.go
│   │   └── inventory/                # downstream servis client'ları
│   │       └── client.go
│   └── transport/
│       └── http/
│           └── routes.go             # Handlers struct + Register(app, h)
├── config/
│   ├── config.yaml
│   └── config.prod.yaml
├── deploy/
│   ├── k8s/
│   ├── prometheus/
│   └── grafana/
├── Dockerfile
├── .dockerignore
├── docker-compose.yml
├── Makefile
├── .golangci.yml
├── CLAUDE.md                         # bkz. 14
└── go.mod
```

### Adımlar

```bash
mkdir product-service && cd product-service
go mod init github.com/hekanemre/product-service
go get github.com/hekanemre/go-core@latest
# core lokaldeyse: go.mod'a replace ekle (bkz. 02)
```

1. `internal/config/config.go` — `BaseConfig`'i squash ile embed et.
2. `config/config.yaml` — app/http/log/tracing + servise özel bölümler.
3. `internal/domain` — entity'ler ve hatalar.
4. `internal/app/<feature>` — interface'ler ve handler'lar.
5. `internal/infra` — implementasyonlar.
6. `internal/transport/http/routes.go` — route kaydı.
7. `cmd/api/main.go` — [10](10-health-lifecycle.md)'daki şablon.
8. Dockerfile, compose, deploy — [11](11-deployment.md)'deki şablonlar.
9. `make tidy lint test run`.

## B. Yeni endpoint ekleme (tarif)

Örnek: `PUT /api/v1/products/:id` — ürün adını güncelle.

### 1) Domain (gerekirse)

```go
// internal/domain/product.go
type Product struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

### 2) Port'a metot ekle

```go
// internal/app/product/ports.go
type Repository interface {
	CreateProduct(ctx context.Context, p *domain.Product) error
	GetProduct(ctx context.Context, id string) (*domain.Product, error)
	UpdateProduct(ctx context.Context, p *domain.Product) error
}
```

### 3) Handler yaz

```go
// internal/app/product/update_product.go
package product

type UpdateProductRequest struct {
	ID   string `params:"id"  validate:"required,uuid4"`
	Name string `json:"name"  validate:"required,min=2,max=120"`
}

type UpdateProductResponse struct {
	Product *domain.Product `json:"product"`
}

type UpdateProductHandler struct {
	repo Repository
	now  func() time.Time
}

func NewUpdateProductHandler(repo Repository) *UpdateProductHandler {
	return &UpdateProductHandler{repo: repo, now: time.Now}
}

func (h *UpdateProductHandler) Handle(ctx context.Context, req *UpdateProductRequest) (*UpdateProductResponse, error) {
	p, err := h.repo.GetProduct(ctx, req.ID)
	if err != nil {
		return nil, err // ErrProductNotFound → 404 otomatik
	}
	p.Name = req.Name
	p.UpdatedAt = h.now().UTC()
	if err := h.repo.UpdateProduct(ctx, p); err != nil {
		return nil, err
	}
	return &UpdateProductResponse{Product: p}, nil
}
```

### 4) Repository implementasyonu

```go
// internal/infra/couchbase/product_repository.go
func (r *ProductRepository) UpdateProduct(ctx context.Context, p *domain.Product) error {
	_, err := r.db.Bucket.DefaultCollection().Replace(p.ID, p, &gocb.ReplaceOptions{Context: ctx})
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return domain.ErrProductNotFound
	}
	if err != nil {
		return fmt.Errorf("couchbase replace product %s: %w", p.ID, err)
	}
	return nil
}
```

### 5) Route kaydı

```go
// internal/transport/http/routes.go
type Handlers struct {
	GetProduct    httpserver.Handler[product.GetProductRequest, product.GetProductResponse]
	CreateProduct httpserver.Handler[product.CreateProductRequest, product.CreateProductResponse]
	UpdateProduct httpserver.Handler[product.UpdateProductRequest, product.UpdateProductResponse]
}

func Register(app *fiber.App, h Handlers) {
	products := app.Group("/api/v1/products")
	products.Get("/:id", httpserver.Handle(h.GetProduct))
	products.Post("/", httpserver.Handle(h.CreateProduct, httpserver.WithStatus(fiber.StatusCreated)))
	products.Put("/:id", httpserver.Handle(h.UpdateProduct))
}
```

### 6) `main.go`'da wiring

```go
handlers := transporthttp.Handlers{
	...
	UpdateProduct: product.NewUpdateProductHandler(productRepo),
}
```

### 7) Test

```go
func TestUpdateProduct_NotFound(t *testing.T) {
	repo := &fakeRepo{getErr: domain.ErrProductNotFound}
	_, err := product.NewUpdateProductHandler(repo).Handle(context.Background(),
		&product.UpdateProductRequest{ID: "x", Name: "yeni"})
	if !errors.Is(err, domain.ErrProductNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
```

## C. Checklist (her endpoint için)

- [ ] Request struct'ında doğru tag'ler: `json` / `params` / `query` / `reqHeader`
- [ ] `validate` tag'leri eklendi
- [ ] Handler sadece interface'lere bağımlı, `fiber` import etmiyor
- [ ] Hatalar domain hatası veya `%w` ile sarılmış
- [ ] `ctx` tüm çağrılara iletildi
- [ ] Başarı status'u doğru (POST → 201, DELETE → 204)
- [ ] Unit test (fake repository ile)
- [ ] Gerekirse iş metriği / span eklendi
- [ ] Downstream çağrısı varsa `httpclient` + idempotency kontrolü

## D. Checklist (her yeni servis için)

- [ ] `app.name`, `app.version` config'te
- [ ] Secret'lar env'den geliyor, yaml'da boş
- [ ] `/livez`, `/readyz`, `/metrics` çalışıyor
- [ ] Readiness'a tüm kritik bağımlılıklar eklendi
- [ ] Tracing Jaeger'da görünüyor, loglarda `trace_id` var
- [ ] Graceful shutdown sırası: http → DB → tracing
- [ ] Dockerfile distroless/non-root, K8s'te probe + limit + PDB
- [ ] CI: vet + lint + test -race + docker build
