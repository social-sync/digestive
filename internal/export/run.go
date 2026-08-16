package export

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/social-sync/grimnir/internal/config"
	"github.com/social-sync/grimnir/internal/manifest"
	"github.com/social-sync/grimnir/internal/source"
	"github.com/social-sync/grimnir/internal/typemap"
	"github.com/social-sync/grimnir/internal/value"
	"github.com/social-sync/grimnir/internal/writer"
)

// Options configures an export run.
type Options struct {
	// RunName overrides the generated (timestamp-based) run directory name.
	RunName string
	// Now stamps the run; the default run name derives from it.
	Now time.Time
	// DeleteOnFailure removes the run directory entirely if the run fails.
	DeleteOnFailure bool
	// Logger receives diagnostic logging; defaults to a no-op logger.
	Logger *slog.Logger
	// Progress receives structured progress events for a live UI; defaults to
	// a no-op reporter. Independent of Logger — a caller typically drives one
	// or the other, not both.
	Progress Reporter
}

// Validate resolves and checks the whole config against the live source
// schema without exporting any data.
func Validate(ctx context.Context, src source.Source, cfg *config.Config) error {
	if err := src.Ping(ctx); err != nil {
		return fmt.Errorf("connect to source: %w", err)
	}
	_, err := buildPlan(ctx, src, cfg)
	return err
}

// Run executes a full export and returns the run directory it produced. The
// Manifest is written only after every table succeeds, so a run directory
// without a manifest.json is an incomplete run.
func Run(ctx context.Context, src source.Source, cfg *config.Config, opts Options) (string, error) {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	rep := opts.Progress
	if rep == nil {
		rep = nopReporter{}
	}
	runStart := time.Now()

	if err := src.Ping(ctx); err != nil {
		return "", fmt.Errorf("connect to source: %w", err)
	}
	plans, err := buildPlan(ctx, src, cfg)
	if err != nil {
		return "", err
	}

	runName := opts.RunName
	if runName == "" {
		runName = opts.Now.UTC().Format("20060102T150405Z")
	}
	runDir := filepath.Join(cfg.Destination.Directory, runName)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", fmt.Errorf("create run directory: %w", err)
	}
	log.Info("export starting", "run", runName, "dir", runDir, "tables", len(plans))

	tableNames := make([]string, len(plans))
	for i, plan := range plans {
		tableNames[i] = plan.cfg.Name
	}
	rep.Start(runName, runDir, tableNames)

	man := &manifest.Manifest{
		Version:   manifest.Version,
		RunID:     runName,
		CreatedAt: opts.Now.UTC().Format(time.RFC3339),
		Source:    manifest.SourceInfo{Engine: "singlestore"},
	}

	for _, plan := range plans {
		rep.TableStart(plan.cfg.Name)
		tableStart := time.Now()
		table, err := exportTable(ctx, src, runDir, plan, log, rep)
		if err != nil {
			log.Error("export failed", "table", plan.cfg.Name, "err", err)
			rep.Failed(plan.cfg.Name, err)
			if opts.DeleteOnFailure {
				if rmErr := os.RemoveAll(runDir); rmErr != nil {
					log.Error("cleanup failed", "dir", runDir, "err", rmErr)
				} else {
					log.Info("run directory removed after failure", "dir", runDir)
				}
			}
			return "", fmt.Errorf("export table %q: %w", plan.cfg.Name, err)
		}
		man.Tables = append(man.Tables, table)
		log.Info("table exported", "table", table.Name, "rows", table.Rows)
		rep.TableDone(table.Name, table.Rows, time.Since(tableStart))
	}

	man.Complete = true
	if err := man.Write(filepath.Join(runDir, "manifest.json")); err != nil {
		if opts.DeleteOnFailure {
			os.RemoveAll(runDir)
		}
		return "", err
	}

	log.Info("export complete", "run", runName, "dir", runDir)
	rep.Done(runName, runDir, time.Since(runStart))
	return runDir, nil
}

// fallbackReporter is implemented by transforms that can silently fall back to
// redaction (json_anonymise), so a run can report how often that happened.
type fallbackReporter interface {
	FallbackCount() int
}

// exportTable streams one table into a Parquet file and returns its Manifest
// entry.
func exportTable(ctx context.Context, src source.Source, runDir string, plan tablePlan, log *slog.Logger, rep Reporter) (manifest.Table, error) {
	fileName := plan.cfg.Name + ".parquet"

	columns := make([]string, len(plan.columns))
	mappings := make([]typemap.Mapping, len(plan.columns))
	for i, cp := range plan.columns {
		columns[i] = cp.col.Name
		mappings[i] = cp.mapping
	}

	pw, err := writer.NewParquet(filepath.Join(runDir, fileName), columns, mappings)
	if err != nil {
		return manifest.Table{}, err
	}

	spec := source.QuerySpec{
		Table:   plan.cfg.Name,
		Columns: columns,
		Where:   plan.cfg.Where,
		OrderBy: plan.cfg.OrderBy,
		Limit:   plan.cfg.Limit,
	}
	rows, err := src.Query(ctx, spec)
	if err != nil {
		pw.Abort()
		return manifest.Table{}, err
	}
	defer rows.Close()

	var written int64
	for rows.Next() {
		cells, err := rows.Scan()
		if err != nil {
			pw.Abort()
			return manifest.Table{}, fmt.Errorf("scan row: %w", err)
		}
		transformed, err := applyTransforms(plan, cells)
		if err != nil {
			pw.Abort()
			return manifest.Table{}, err
		}
		if err := pw.WriteRow(transformed); err != nil {
			pw.Abort()
			return manifest.Table{}, err
		}
		written++
		if written%rowTickInterval == 0 {
			rep.TableRows(plan.cfg.Name, written)
		}
	}
	if err := rows.Err(); err != nil {
		pw.Abort()
		return manifest.Table{}, fmt.Errorf("iterate rows: %w", err)
	}
	if err := pw.Close(); err != nil {
		return manifest.Table{}, err
	}

	// Surface any json_anonymise cells that failed to parse and were redacted
	// whole — a large count usually means the transform is on the wrong column.
	for _, cp := range plan.columns {
		if fr, ok := cp.xform.(fallbackReporter); ok {
			if n := fr.FallbackCount(); n > 0 {
				log.Warn("json_anonymise redacted unparseable cells",
					"table", plan.cfg.Name, "column", cp.col.Name, "cells", n)
				rep.Warn(fmt.Sprintf("%s.%s: json_anonymise redacted %d unparseable cell(s)",
					plan.cfg.Name, cp.col.Name, n))
			}
		}
	}

	return manifest.Table{
		Name:    plan.cfg.Name,
		File:    fileName,
		Rows:    pw.Rows(),
		Where:   plan.cfg.Where,
		OrderBy: plan.cfg.OrderBy,
		Limit:   plan.cfg.Limit,
		Columns: manifestColumns(plan),
	}, nil
}

// applyTransforms runs each column's transform (if any) over one row.
func applyTransforms(plan tablePlan, cells []value.Value) ([]value.Value, error) {
	if len(cells) != len(plan.columns) {
		return nil, fmt.Errorf("row has %d cells, expected %d columns", len(cells), len(plan.columns))
	}
	out := make([]value.Value, len(cells))
	for i, cp := range plan.columns {
		if cp.xform == nil {
			out[i] = cells[i]
			continue
		}
		v, err := cp.xform.Transform(cells[i])
		if err != nil {
			return nil, fmt.Errorf("transform column %q: %w", cp.col.Name, err)
		}
		out[i] = v
	}
	return out, nil
}

func manifestColumns(plan tablePlan) []manifest.Column {
	cols := make([]manifest.Column, len(plan.columns))
	for i, cp := range plan.columns {
		sourceType := cp.col.DataType
		if cp.col.Unsigned {
			sourceType += " unsigned"
		}
		cols[i] = manifest.Column{
			Name:        cp.col.Name,
			SourceType:  sourceType,
			Nullable:    cp.col.Nullable,
			ParquetType: cp.mapping.Physical(),
			Lossless:    cp.mapping.Lossless,
			Transform:   cp.xformName,
		}
	}
	return cols
}
