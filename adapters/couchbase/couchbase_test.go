package couchbase_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/couchbase/gocb/v2"

	"github.com/keskinhakanemre/go-core/adapters/couchbase"
)

func TestConnectRequiresBucket(t *testing.T) {
	if _, err := couchbase.Connect(couchbase.Config{URL: "couchbase://localhost"}, nil); err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestErrorHelpers(t *testing.T) {
	if !couchbase.IsNotFound(fmt.Errorf("get: %w", gocb.ErrDocumentNotFound)) {
		t.Fatal("IsNotFound")
	}
	if !couchbase.IsExists(fmt.Errorf("insert: %w", gocb.ErrDocumentExists)) {
		t.Fatal("IsExists")
	}
}

func TestParentSpan(t *testing.T) {
	if couchbase.ParentSpan(context.Background()) == nil {
		t.Fatal("expected a span wrapper")
	}
}
