package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	buildsservice "janus/internal/services/builds"
	identityservice "janus/internal/services/identity"
	projectsservice "janus/internal/services/projects"
)

// migrateUsage is printed for a `janus migrate` that names no known migration.
const migrateUsage = `usage: janus migrate split

split  copies the tables each service now owns out of JANUS_DATABASE_PATH
       (data/janus.db): the identity tables into a new
       JANUS_IDENTITY_DATABASE_PATH (data/identity.db) and the project tables
       into a new JANUS_PROJECTS_DATABASE_PATH (data/projects.db), and the WASM
       build tables into a new JANUS_BUILDS_DATABASE_PATH (data/builds.db). A service
       whose database exists is skipped, so running it again only does what is
       left. Stop janus first.`

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
	splits := []struct {
		service, path string
		split         func(ctx context.Context, legacy, path string) (map[string]int, error)
	}{
		{"identity", cfg.IdentityDatabasePath, func(ctx context.Context, legacy, path string) (map[string]int, error) {
			return identityservice.Split(ctx, legacy, path)
		}},
		{"projects", cfg.ProjectsDatabasePath, func(ctx context.Context, legacy, path string) (map[string]int, error) {
			return projectsservice.Split(ctx, legacy, path)
		}},
		{"builds", cfg.BuildsDatabasePath, func(ctx context.Context, legacy, path string) (map[string]int, error) {
			return buildsservice.Split(ctx, legacy, path)
		}},
	}
	for _, step := range splits {
		if _, err := os.Stat(step.path); err == nil {
			fmt.Fprintf(out, "%s: %s exists, skipped\n", step.service, step.path)
			continue
		}
		copied, err := step.split(ctx, cfg.DatabasePath, step.path)
		if err != nil {
			return fmt.Errorf("split %s for the %s service: %w", cfg.DatabasePath, step.service, err)
		}
		names := make([]string, 0, len(copied))
		for name := range copied {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(out, "%s: copied into %s:\n", step.service, step.path)
		for _, name := range names {
			fmt.Fprintf(out, "  %-20s %d rows\n", name, copied[name])
		}
	}
	fmt.Fprintf(out, "%s is unchanged; what was copied out is no longer read from it.\n", cfg.DatabasePath)
	return nil
}
