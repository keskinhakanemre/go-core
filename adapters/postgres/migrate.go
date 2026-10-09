package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// MigrationResult describes one applied migration.
type MigrationResult struct {
	Version  int64
	Source   string // file name, e.g. 00001_init.sql
	Duration time.Duration
}

// Migrate applies the pending goose migrations in fsys (SQL files at its
// root, e.g. fs.Sub of an embedded "migrations" directory). A PostgreSQL
// advisory lock makes it safe when several replicas start at the same time.
//
//	//go:embed migrations/*.sql
//	var migrations embed.FS
//
//	sub, _ := fs.Sub(migrations, "migrations")
//	applied, err := postgres.Migrate(ctx, pool, sub)
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]MigrationResult, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}
	res, err := p.Up(ctx)
	out := make([]MigrationResult, 0, len(res))
	for _, r := range res {
		m := MigrationResult{Duration: r.Duration}
		if r.Source != nil {
			m.Version, m.Source = r.Source.Version, r.Source.Path
		}
		out = append(out, m)
	}
	if err != nil {
		return out, fmt.Errorf("postgres: migrate: %w", err)
	}
	return out, nil
}
