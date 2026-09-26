# 11 — Deployment: Docker, Compose, Kubernetes, Prometheus, Grafana

## Dockerfile

Kaynak projede: multi-stage (`golang:alpine` → `alpine`), `CGO_ENABLED=0`, non-root kullanıcı, config dosyası image'a kopyalanıyor. İyi bir temel.

Core template'i (iyileştirilmiş):

```dockerfile
# syntax=docker/dockerfile:1
ARG GO_VERSION=1.23

FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/app ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/app /app/app
COPY config/ /app/config/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/app"]
```

Farklar: build cache mount'ları, `-trimpath -s -w` (küçük binary), distroless (shell yok, daha az saldırı yüzeyi), `cmd/api` giriş noktası, config klasörü `/app/config` altında (config loader `./config` yolunu arıyor).

`.dockerignore`:

```
.git
bin/
deploy/
*.md
.env
```

## docker-compose.yml (lokal geliştirme ortamı)

Kaynak projede: `app`, `prometheus`, `grafana`, `jaeger` (OTLP 4317/4318 açık). **Not:** `app` servisine verilen `JAEGER_AGENT_HOST/PORT` env'leri kod tarafından hiç okunmuyor (kod OTLP kullanıyor ve endpoint'i config.yaml'dan alıyor); ayrıca Couchbase compose'da yok.

Core template'i:

```yaml
services:
  app:
    build: .
    ports: ["8080:8080"]
    environment:
      APP_ENV: local
      APP_TRACING_ENDPOINT: jaeger:4318
      APP_COUCHBASE_URL: couchbase://couchbase
      APP_COUCHBASE_PASSWORD: ${COUCHBASE_PASSWORD:-password}
    depends_on: [jaeger, couchbase]

  couchbase:            # veya postgres
    image: couchbase:community
    ports: ["8091-8096:8091-8096", "11210:11210"]

  # postgres:
  #   image: postgres:16-alpine
  #   environment: { POSTGRES_PASSWORD: postgres, POSTGRES_DB: app }
  #   ports: ["5432:5432"]

  jaeger:
    image: jaegertracing/all-in-one:latest
    environment:
      COLLECTOR_OTLP_ENABLED: "true"
    ports:
      - "16686:16686"   # UI
      - "4317:4317"     # OTLP gRPC
      - "4318:4318"     # OTLP HTTP

  prometheus:
    image: prom/prometheus:latest
    ports: ["9090:9090"]
    volumes:
      - ./deploy/prometheus/prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus_data:/prometheus

  grafana:
    image: grafana/grafana:latest
    ports: ["3000:3000"]
    environment:
      GF_SECURITY_ADMIN_USER: admin
      GF_SECURITY_ADMIN_PASSWORD: admin
    volumes:
      - grafana_data:/var/lib/grafana
      - ./deploy/grafana:/etc/grafana/provisioning/
      - ./deploy/grafana/dashboards:/var/lib/grafana/dashboards
    depends_on: [prometheus]

volumes:
  prometheus_data:
  grafana_data:
```

URL'ler: Jaeger UI http://localhost:16686 · Prometheus http://localhost:9090 · Grafana http://localhost:3000

## Prometheus & Grafana provisioning

Kaynak projeden birebir alınabilir:

`deploy/prometheus/prometheus.yml`
```yaml
global:
  scrape_interval: 10s
scrape_configs:
  - job_name: go-app
    metrics_path: /metrics
    static_configs:
      - targets: ["app:8080"]
```

`deploy/grafana/datasources/prometheus-datasource.yml`
```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    jsonData: { httpMethod: POST }
```

`deploy/grafana/dashboards/main.yaml`
```yaml
apiVersion: 1
providers:
  - name: "Dashboard provider"
    orgId: 1
    type: file
    updateIntervalSeconds: 10
    options:
      path: /var/lib/grafana/dashboards
      foldersFromFilesStructure: true
```

Dashboard JSON: kaynak projedeki `.deploy/grafana/dashboards/10826_migrated.json` (Go Metrics, grafana.com ID 10826) kopyalanır. Buna ek olarak HTTP RED dashboard'u ([07](07-observability.md)'deki PromQL'lerle) oluşturulabilir.

## Kubernetes

Kaynak projede: `Deployment` (sadece CPU request/limit), `Service` (80 → 8080), `HPA` (CPU %5 hedefi — demo amaçlı çok düşük), minikube için `metric-server-patch.yaml`.

**Eksikler:** probe yok, memory limit yok, config/secret yok, securityContext yok, `terminationGracePeriodSeconds` yok.

Core template'i `deploy/k8s/`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: <servis-adi>
  labels: { app: <servis-adi> }
spec:
  replicas: 2
  selector:
    matchLabels: { app: <servis-adi> }
  template:
    metadata:
      labels: { app: <servis-adi> }
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      terminationGracePeriodSeconds: 30
      securityContext:
        runAsNonRoot: true
      containers:
        - name: app
          image: <registry>/<servis-adi>:<tag>
          ports: [{ containerPort: 8080, name: http }]
          env:
            - { name: APP_ENV, value: prod }
            - { name: APP_TRACING_ENDPOINT, value: otel-collector.observability:4318 }
          envFrom:
            - secretRef: { name: <servis-adi>-secrets }   # APP_COUCHBASE_PASSWORD vb.
          resources:
            requests: { cpu: 100m, memory: 128Mi }
            limits:   { cpu: 500m, memory: 256Mi }
          readinessProbe:
            httpGet: { path: /readyz, port: http }
            periodSeconds: 5
            failureThreshold: 3
          livenessProbe:
            httpGet: { path: /livez, port: http }
            initialDelaySeconds: 5
            periodSeconds: 10
          startupProbe:
            httpGet: { path: /livez, port: http }
            failureThreshold: 30
            periodSeconds: 2
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: { drop: ["ALL"] }
---
apiVersion: v1
kind: Service
metadata:
  name: <servis-adi>
spec:
  selector: { app: <servis-adi> }
  ports: [{ port: 80, targetPort: http, protocol: TCP }]
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: <servis-adi>
spec:
  scaleTargetRef: { apiVersion: apps/v1, kind: Deployment, name: <servis-adi> }
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Resource
      resource: { name: cpu, target: { type: Utilization, averageUtilization: 70 } }
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 0
      policies: [{ type: Percent, value: 100, periodSeconds: 15 }]
    scaleDown:
      stabilizationWindowSeconds: 120
      policies: [{ type: Percent, value: 50, periodSeconds: 30 }]
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: <servis-adi>
spec:
  minAvailable: 1
  selector:
    matchLabels: { app: <servis-adi> }
```

> Go 1.25+ container CPU limitini `GOMAXPROCS` için otomatik dikkate alır. Daha eski Go sürümlerinde `go.uber.org/automaxprocs` import edilmeli (`import _ "go.uber.org/automaxprocs"`). Memory limit için `GOMEMLIMIT` env'ini limitin ~%90'ı olarak ayarla (ör. `GOMEMLIMIT=230MiB`).

Lokal K8s (minikube) için kaynak projedeki `metric-server-patch.yaml` HPA'nın çalışması için gerekli.

## CI (öneri — GitHub Actions)

```yaml
name: ci
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: go vet ./...
      - uses: golangci/golangci-lint-action@v6
      - run: go test -race -coverprofile=cover.out ./...
      - run: docker build -t app:${{ github.sha }} .
```
