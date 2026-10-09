package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/keskinhakanemre/go-core/adapters/postgres"
)

func TestConnectInvalidDSNDoesNotLeakPassword(t *testing.T) {
	_, err := postgres.Connect(context.Background(), postgres.Config{DSN: "postgres://u:topsecret@host:notaport/db"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "topsecret") {
		t.Fatalf("password leaked: %v", err)
	}
}

func TestConnectUnreachable(t *testing.T) {
	_, err := postgres.Connect(context.Background(), postgres.Config{
		DSN:            "postgres://u:p@127.0.0.1:1/db?sslmode=disable",
		ConnectTimeout: 500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestErrorHelpers(t *testing.T) {
	if !postgres.IsNoRows(fmt.Errorf("repo: %w", pgx.ErrNoRows)) {
		t.Fatal("IsNoRows")
	}
	dup := fmt.Errorf("insert: %w", &pgconn.PgError{Code: postgres.CodeUniqueViolation})
	if !postgres.IsUniqueViolation(dup) || postgres.HasCode(dup, postgres.CodeCheckViolation) {
		t.Fatal("HasCode")
	}
	if postgres.IsUniqueViolation(errors.New("x")) {
		t.Fatal("plain errors are not pg errors")
	}
}

// TestIntegration runs against a real database when POSTGRES_TEST_DSN is set
// (CI provides one as a service container).
func TestIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := postgres.Connect(ctx, postgres.Config{DSN: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	// Temp tables are per connection; pin one connection for the rest of the test.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE items (id text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	err = postgres.WithTx(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO items (id) VALUES ('a')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO items (id) VALUES ('a')`)
	if !postgres.IsUniqueViolation(err) {
		t.Fatalf("expected unique violation, got %v", err)
	}
	var id string
	err = conn.QueryRow(ctx, `SELECT id FROM items WHERE id = 'missing'`).Scan(&id)
	if !postgres.IsNoRows(err) {
		t.Fatalf("expected no rows, got %v", err)
	}
}

type fakeTx struct {
	pgx.Tx
	committed, rolledBack bool
}

func (f *fakeTx) Commit(context.Context) error   { f.committed = true; return nil }
func (f *fakeTx) Rollback(context.Context) error { f.rolledBack = true; return nil }

type fakeDB struct{ tx *fakeTx }

func (f *fakeDB) Begin(context.Context) (pgx.Tx, error) { f.tx = &fakeTx{}; return f.tx, nil }

func TestWithTx(t *testing.T) {
	db := &fakeDB{}
	if err := postgres.WithTx(context.Background(), db, func(pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !db.tx.committed || db.tx.rolledBack {
		t.Fatal("expected commit")
	}

	boom := errors.New("boom")
	if err := postgres.WithTx(context.Background(), db, func(pgx.Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if db.tx.committed || !db.tx.rolledBack {
		t.Fatal("expected rollback")
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic must propagate")
			}
		}()
		_ = postgres.WithTx(context.Background(), db, func(pgx.Tx) error { panic("x") })
	}()
	if !db.tx.rolledBack {
		t.Fatal("expected rollback on panic")
	}
}

func TestConstraintName(t *testing.T) {
	err := fmt.Errorf("insert: %w", &pgconn.PgError{Code: postgres.CodeUniqueViolation, ConstraintName: "items_name_key"})
	if got := postgres.ConstraintName(err); got != "items_name_key" {
		t.Fatalf("ConstraintName = %q", got)
	}
	if got := postgres.ConstraintName(errors.New("other")); got != "" {
		t.Fatalf("ConstraintName(other) = %q", got)
	}
}

// TestMigrate applies migrations twice (the second run is a no-op) against a
// real database when POSTGRES_TEST_DSN is set.
func TestMigrate(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := postgres.Connect(ctx, postgres.Config{DSN: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS migrate_items, goose_db_version`)
	})

	fsys := fstest.MapFS{
		"00001_items.sql": {Data: []byte("-- +goose Up\nCREATE TABLE migrate_items (id text PRIMARY KEY);\n-- +goose Down\nDROP TABLE migrate_items;\n")},
		"00002_name.sql":  {Data: []byte("-- +goose Up\nALTER TABLE migrate_items ADD COLUMN name text;\n-- +goose Down\nALTER TABLE migrate_items DROP COLUMN name;\n")},
	}
	applied, err := postgres.Migrate(ctx, pool, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[0].Version != 1 || applied[1].Source != "00002_name.sql" {
		t.Fatalf("applied = %+v", applied)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO migrate_items (id, name) VALUES ('a', 'A')`); err != nil {
		t.Fatal(err)
	}
	again, err := postgres.Migrate(ctx, pool, fsys)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run: %+v, %v", again, err)
	}
}
