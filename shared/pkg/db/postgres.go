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
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid dsn: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnIdleTime = 5 * time.Minute
	config.MaxConnLifetime = 30 * time.Minute

	// Create the pool once — NewWithConfig does not open connections immediately.
	// Retrying NewWithConfig in a loop leaks pools (each spawns MinConns background
	// goroutines), which exhausts the DB's connection limit.
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid config: %w", err)
	}

	var lastErr error
	for i := 0; i < 30; i++ {
		if pingErr := pool.Ping(ctx); pingErr == nil {
			log.Println("Connected to PostgreSQL")
			return &DB{Pool: pool}, nil
		} else {
			lastErr = pingErr
		}
		log.Printf("Waiting for PostgreSQL... (%d/30): %v", i+1, lastErr)
		time.Sleep(2 * time.Second)
	}
	pool.Close()
	return nil, fmt.Errorf("postgres: failed after 30 attempts: %w", lastErr)
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
		if err := d.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=$1", file).Scan(&count); err != nil {
			return fmt.Errorf("check migration %s: %w", file, err)
		}
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

// MustConnect connects to Postgres, runs migrations, and returns the pool.
// Calls log.Fatal on any error — intended for use in main/container startup.
func MustConnect(ctx context.Context, dsn string, migrationsFS fs.FS) *pgxpool.Pool {
	database, err := Connect(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := database.RunMigrations(ctx, migrationsFS); err != nil {
		log.Fatal(err)
	}
	return database.Pool
}
