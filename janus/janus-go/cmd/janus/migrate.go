package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	identityservice "janus/internal/services/identity"
)

// migrateUsage is printed for a `janus migrate` that names no known migration.
const migrateUsage = `usage: janus migrate split

split  copies the identity tables of JANUS_DATABASE_PATH (data/janus.db) into
       a new JANUS_IDENTITY_DATABASE_PATH (data/identity.db) for the identity
       service. It runs once: it refuses when the identity database exists.
       Stop janus first.`

// runMigrate runs `janus migrate <args>` and writes what it did to out.
func runMigrate(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "split" {
		return errors.New(migrateUsage)
	}
	if err := loadEnvironment(); err != nil {
		return fmt.Errorf("load environment: %w", err)
	}
	cfg, err := loadSettings(ctx)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	copied, err := identityservice.Split(ctx, cfg.DatabasePath, cfg.IdentityDatabasePath)
	if err != nil {
		return fmt.Errorf("split %s: %w", cfg.DatabasePath, err)
	}
	tables := make([]string, 0, len(copied))
	for table := range copied {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	fmt.Fprintf(out, "copied into %s:\n", cfg.IdentityDatabasePath)
	for _, table := range tables {
		fmt.Fprintf(out, "  %-20s %d rows\n", table, copied[table])
	}
	fmt.Fprintf(out, "%s is unchanged; its identity tables are no longer read.\n", cfg.DatabasePath)
	return nil
}
