package db

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgx connection pool.
type DB struct {
	Pool *pgxpool.Pool
}

// Connect opens a connection pool with retry logic.
func Connect(ctx context.Context, dsn string) (*DB, error) {
	var pool *pgxpool.Pool
	var err error
	for i := 0; i < 30; i++ {
		pool, err = pgxpool.New(ctx, dsn)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				log.Println("Connected to PostgreSQL")
				return &DB{Pool: pool}, nil
			}
		}
		log.Printf("Waiting for PostgreSQL... (%d/30)", i+1)
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("postgres: failed after 30 attempts: %w", err)
}

// RunMigrations reads SQL files from the embedded FS and applies them in order.
func (d *DB) RunMigrations(ctx context.Context, migrationFS fs.FS) error {
	_, err := d.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ  DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	sort.Strings(sqlFiles)

	for _, file := range sqlFiles {
		var count int
		_ = d.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=$1", file).Scan(&count)
		if count > 0 {
			log.Printf("Migration %s — already applied", file)
			continue
		}

		content, err := fs.ReadFile(migrationFS, file)
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if _, err = d.Pool.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("exec %s: %w", file, err)
		}
		if _, err = d.Pool.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", file); err != nil {
			return fmt.Errorf("record %s: %w", file, err)
		}
		log.Printf("Applied migration: %s", file)
	}
	return nil
}

// Close shuts down the pool.
func (d *DB) Close() { d.Pool.Close() }
