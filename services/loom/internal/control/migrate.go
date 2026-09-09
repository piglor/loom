package control

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql legacy/*.sql
var migrations embed.FS

// Migrate upgrades the generic database. Legacy provider tables are only
// upgraded when they already exist; a fresh install never creates them.
// Disable old writers and back up the database before this cutover.
func (s *Store) Migrate(ctx context.Context) error {
	return s.tx(ctx, func(t *transaction) error {
		if _, err := t.sql.ExecContext(ctx, "SELECT pg_advisory_xact_lock(73021001)"); err != nil {
			return err
		}
		if _, err := t.sql.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT clock_timestamp())"); err != nil {
			return err
		}
		var legacy bool
		if err := t.sql.QueryRowContext(ctx, "SELECT to_regclass('github_bindings') IS NOT NULL").Scan(&legacy); err != nil {
			return err
		}
		paths := map[int]string{}
		dirs := []string{"migrations"}
		if legacy {
			dirs = append(dirs, "legacy")
		}
		for _, dir := range dirs {
			entries, err := migrations.ReadDir(dir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				version, err := strconv.Atoi(strings.Split(entry.Name(), "-")[0])
				if err != nil {
					return err
				}
				if dir == "legacy" && version == 3 {
					continue
				}
				if _, exists := paths[version]; exists {
					return fmt.Errorf("duplicate migration version %d", version)
				}
				paths[version] = dir + "/" + entry.Name()
			}
		}
		versions := make([]int, 0, len(paths))
		for v := range paths {
			versions = append(versions, v)
		}
		sort.Ints(versions)
		for _, version := range versions {
			var exists bool
			if err := t.sql.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			body, err := migrations.ReadFile(paths[version])
			if err != nil {
				return err
			}
			if _, err = t.sql.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("migration %d: %w", version, err)
			}
		}
		return nil
	})
}
