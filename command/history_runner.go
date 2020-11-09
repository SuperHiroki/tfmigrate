package command

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	"github.com/minamijoyo/tfmigrate/config"
	"github.com/minamijoyo/tfmigrate/history"
	"github.com/minamijoyo/tfmigrate/tfmigrate"
)

// HistoryRunner is a history-aware runner.
// It allows us to apply all unapplied migrations and save them to history.
type HistoryRunner struct {
	// A path to migration file. This is optional.
	// If set, run a single migration and save it to history.
	// If not set, run all unapplied migrations and save them to history.
	filename string
	// A global configuration.
	config *config.TfmigrateConfig
	// A option to share across migrations.
	option *tfmigrate.MigratorOption
	// A controller which manages history.
	hc *history.Controller
}

// NewHistoryRunner returns a new HistoryRunner instance.
func NewHistoryRunner(ctx context.Context, filename string, config *config.TfmigrateConfig, option *tfmigrate.MigratorOption) (*HistoryRunner, error) {
	hc, err := history.NewController(ctx, config.History)
	if err != nil {
		return nil, err
	}

	r := &HistoryRunner{
		filename: filename,
		config:   config,
		option:   option,
		hc:       hc,
	}

	return r, nil
}

// Plan plans migrations with history-aware mode.
// If a filename is set, run a single migration.
// If not set, run all unapplied migrations.
func (r *HistoryRunner) Plan(ctx context.Context) error {
	if len(r.filename) != 0 {
		// file mode
		return r.planFile(ctx, r.filename)
	}

	// directory mode
	return r.planDir(ctx)
}

// planFile plans a single migration.
func (r *HistoryRunner) planFile(ctx context.Context, filename string) error {
	if r.hc.AlreadyApplied(filename) {
		return fmt.Errorf("a migration has already been applied: %s", filename)
	}

	path := r.resolvePath(filename)
	fr, err := NewFileRunner(path, r.option)
	if err != nil {
		return err
	}

	return fr.Plan(ctx)
}

// planDir plans all unapplied migrations.
func (r *HistoryRunner) planDir(ctx context.Context) error {
	unapplied := r.hc.UnappliedMigrations()
	log.Printf("[INFO] [runner] unapplied migration files: %v\n", unapplied)

	if len(unapplied) == 0 {
		log.Printf("[INFO] [runner] no unapplied migrations\n")
		return nil
	}

	for _, filename := range unapplied {
		err := r.planFile(ctx, filename)
		if err != nil {
			return err
		}
	}

	return nil
}

// Apply applis migrations and save them to history.
// If a filename is set, run a single migration.
// If not set, run all unapplied migrations.
func (r *HistoryRunner) Apply(ctx context.Context) (err error) {
	// save history on exit
	beforeLen := r.hc.HistoryLength()
	defer func() {
		// if the number of records in history doesn't change,
		// we don't want to update a timestamp of history file.
		afterLen := r.hc.HistoryLength()
		if beforeLen == afterLen {
			return
		}

		// be sure not to overwrite an original error generated by outside of defer
		serr := r.hc.Save(ctx)
		if serr == nil {
			log.Printf("[INFO] [runner] history saved\n")
			return
		}

		// return a named error from defer
		log.Printf("[ERROR] [runner] failed to save history. The history may be inconsistent\n")
		if err == nil {
			err = fmt.Errorf("apply succeed, but failed to save history: %v", serr)
			return
		}
		err = fmt.Errorf("failed to save history: %v, failed to apply: %v", serr, err)
	}()

	if len(r.filename) != 0 {
		// file mode
		err = r.applyFile(ctx, r.filename)
		return err
	}

	// directory mode
	err = r.applyDir(ctx)
	return err
}

// applyFile applies a single migration.
func (r *HistoryRunner) applyFile(ctx context.Context, filename string) error {
	if r.hc.AlreadyApplied(filename) {
		return fmt.Errorf("a migration has already been applied: %s", filename)
	}

	path := r.resolvePath(filename)
	fr, err := NewFileRunner(path, r.option)
	if err != nil {
		return err
	}

	err = fr.Apply(ctx)
	if err != nil {
		return err
	}

	mc := fr.MigrationConfig()
	log.Printf("[INFO] [runner] add a record to history: %s, type: %s, name: %s\n", filename, mc.Type, mc.Name)
	r.hc.AddRecord(filename, mc.Type, mc.Name, nil)

	return nil
}

// applyDir appies all unapplied migrations.
func (r *HistoryRunner) applyDir(ctx context.Context) (err error) {
	unapplied := r.hc.UnappliedMigrations()
	log.Printf("[INFO] [runner] unapplied migration files: %v\n", unapplied)

	if len(unapplied) == 0 {
		log.Printf("[INFO] [runner] no unapplied migrations\n")
		return nil
	}

	for _, filename := range unapplied {
		err := r.applyFile(ctx, filename)
		if err != nil {
			return err
		}
	}

	return nil
}

// resolvePath returns a path of migration file in migration dir.
func (r *HistoryRunner) resolvePath(filename string) string {
	return filepath.Join(r.config.History.MigrationDir, filename)
}
