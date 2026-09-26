// Package postgres opens a traced pgx connection pool and offers a
// transaction helper. It is a separate Go module so that services that do not
// use PostgreSQL do not pull in its dependencies.
//
// *pgxpool.Pool implements Ping(ctx) error and can be registered directly as a
// readiness check: svc.Health.Add("postgres", pool).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	DSN               string        // postgres://user:pass@host:5432/db?sslmode=disable
	MaxConns          int32         // default pgx: max(4, NumCPU)
	MinConns          int32         // default 0
	MaxConnLifetime   time.Duration // default pgx: 1h
	MaxConnIdleTime   time.Duration // default pgx: 30m
	HealthCheckPeriod time.Duration // default pgx: 1m
	ConnectTimeout    time.Duration // initial connect + ping, default 5s
}

// Connect creates the pool, installs OpenTelemetry tracing and verifies
// connectivity with a ping, failing fast on startup.
func Connect(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		// The parse error may echo the DSN (including password); do not wrap it.
		return nil, errors.New("postgres: invalid DSN")
	}
	if cfg.MaxConns > 0 {
		pc.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pc.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		pc.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		pc.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		pc.HealthCheckPeriod = cfg.HealthCheckPeriod
	}
	pc.ConnConfig.Tracer = otelpgx.NewTracer()

	timeout := cfg.ConnectTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping %s:%d: %w", pc.ConnConfig.Host, pc.ConnConfig.Port, err)
	}
	return pool, nil
}

// Close adapts pool.Close to a shutdown hook signature.
func Close(pool *pgxpool.Pool) func(context.Context) error {
	return func(context.Context) error {
		pool.Close()
		return nil
	}
}

// Beginner is satisfied by *pgxpool.Pool and pgx.Tx (for nested savepoints).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// WithTx runs fn in a transaction. It commits when fn returns nil and rolls
// back on error or panic.
func WithTx(ctx context.Context, db Beginner, fn func(tx pgx.Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

// Common SQLSTATE codes, see https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	CodeUniqueViolation     = "23505"
	CodeForeignKeyViolation = "23503"
	CodeNotNullViolation    = "23502"
	CodeCheckViolation      = "23514"
	CodeSerialization       = "40001"
)

// IsNoRows reports whether err is pgx.ErrNoRows.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// HasCode reports whether err is a PostgreSQL error with the given SQLSTATE.
func HasCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// IsUniqueViolation reports whether err is a unique constraint violation.
func IsUniqueViolation(err error) bool { return HasCode(err, CodeUniqueViolation) }
