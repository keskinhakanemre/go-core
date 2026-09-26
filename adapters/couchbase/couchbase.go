// Package couchbase opens a traced Couchbase cluster/bucket connection. It is
// a separate Go module so that services that do not use Couchbase do not pull
// in its dependencies.
//
// *DB implements Ping(ctx) error and can be registered as a readiness check.
package couchbase

import (
	"context"
	"errors"
	"fmt"
	"time"

	gocbopentelemetry "github.com/couchbase/gocb-opentelemetry"
	"github.com/couchbase/gocb/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	URL      string // couchbase://host
	Username string
	Password string
	Bucket   string
	Timeout  time.Duration // connect/KV/query timeout, default 3s
}

type DB struct {
	Cluster *gocb.Cluster
	Bucket  *gocb.Bucket
}

// Connect connects to the cluster and waits until the bucket is ready. tp may
// be nil, in which case the global TracerProvider is used.
func Connect(cfg Config, tp trace.TracerProvider) (*DB, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("couchbase: bucket is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if tp == nil {
		tp = otel.GetTracerProvider()
	}

	cluster, err := gocb.Connect(cfg.URL, gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{Username: cfg.Username, Password: cfg.Password},
		TimeoutsConfig: gocb.TimeoutsConfig{
			ConnectTimeout: cfg.Timeout,
			KVTimeout:      cfg.Timeout,
			QueryTimeout:   cfg.Timeout,
		},
		Transcoder: gocb.NewJSONTranscoder(),
		Tracer:     gocbopentelemetry.NewOpenTelemetryRequestTracer(tp),
	})
	if err != nil {
		return nil, fmt.Errorf("couchbase: connect: %w", err)
	}
	bucket := cluster.Bucket(cfg.Bucket)
	if err := bucket.WaitUntilReady(cfg.Timeout, nil); err != nil {
		_ = cluster.Close(nil)
		return nil, fmt.Errorf("couchbase: bucket %q not ready: %w", cfg.Bucket, err)
	}
	return &DB{Cluster: cluster, Bucket: bucket}, nil
}

// Ping implements health.Checker.
func (d *DB) Ping(ctx context.Context) error {
	rep, err := d.Bucket.Ping(&gocb.PingOptions{Context: ctx})
	if err != nil {
		return err
	}
	for svc, results := range rep.Services {
		for _, r := range results {
			if r.State != gocb.PingStateOk {
				return fmt.Errorf("couchbase: %v endpoint %s state %v", svc, r.Remote, r.State)
			}
		}
	}
	return nil
}

// Close implements the shutdown hook signature.
func (d *DB) Close(context.Context) error { return d.Cluster.Close(nil) }

// ParentSpan links Couchbase spans to the span in ctx. Pass it as the
// ParentSpan option of gocb operations:
//
//	coll.Get(id, &gocb.GetOptions{Context: ctx, ParentSpan: couchbase.ParentSpan(ctx)})
func ParentSpan(ctx context.Context) gocb.RequestSpan {
	return gocbopentelemetry.NewOpenTelemetryRequestSpan(ctx, trace.SpanFromContext(ctx))
}

// IsNotFound reports whether err means the document does not exist.
func IsNotFound(err error) bool { return errors.Is(err, gocb.ErrDocumentNotFound) }

// IsExists reports whether err means the document already exists.
func IsExists(err error) bool { return errors.Is(err, gocb.ErrDocumentExists) }
