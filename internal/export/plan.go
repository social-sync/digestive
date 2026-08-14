// Package export orchestrates a run: it plans and validates the work against
// the live source schema, then streams each table through its transforms into
// a Parquet file and records a Manifest.
package export

import (
	"context"
	"fmt"

	"github.com/danmatthews/sql-exporter/internal/config"
	"github.com/danmatthews/sql-exporter/internal/source"
	"github.com/danmatthews/sql-exporter/internal/transform"
	"github.com/danmatthews/sql-exporter/internal/typemap"
)

// columnPlan is the resolved plan for one column of a table.
type columnPlan struct {
	col       source.Column
	mapping   typemap.Mapping
	xform     transform.Transformer // nil when the column passes through
	xformName string
}

// tablePlan is the resolved plan for one table.
type tablePlan struct {
	cfg     config.TableConfig
	columns []columnPlan
}

// buildPlan resolves and validates every table in the config against the live
// source schema. It is the shared core of both validate and export, so a
// validate run catches the same errors an export would, without writing data.
func buildPlan(ctx context.Context, src source.Source, cfg *config.Config) ([]tablePlan, error) {
	if len(cfg.Tables) == 0 {
		return nil, fmt.Errorf("no tables configured for export")
	}
	hashKey := []byte(cfg.Hashing.Key)

	plans := make([]tablePlan, 0, len(cfg.Tables))
	for _, t := range cfg.Tables {
		if t.Name == "" {
			return nil, fmt.Errorf("table entry with no name")
		}
		cols, err := src.Columns(ctx, t.Name)
		if err != nil {
			return nil, err
		}
		byName := make(map[string]source.Column, len(cols))
		for _, c := range cols {
			byName[c.Name] = c
		}

		// Every configured column must exist and validate.
		xforms := make(map[string]transform.Transformer, len(t.Columns))
		for colName, cc := range t.Columns {
			col, ok := byName[colName]
			if !ok {
				return nil, fmt.Errorf("table %q has no column %q (referenced in config)", t.Name, colName)
			}
			if err := validateColumnTransform(t.Name, col, cc, hashKey); err != nil {
				return nil, err
			}
			xf, err := transform.Build(specFromConfig(cc), hashKey)
			if err != nil {
				return nil, fmt.Errorf("table %q column %q: %w", t.Name, colName, err)
			}
			xforms[colName] = xf
		}

		plan := tablePlan{cfg: t}
		for _, col := range cols {
			plan.columns = append(plan.columns, columnPlan{
				col:       col,
				mapping:   typemap.Map(col.DataType, col.Unsigned),
				xform:     xforms[col.Name],
				xformName: t.Columns[col.Name].Transform,
			})
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// validateColumnTransform enforces the rules a transform places on its column.
func validateColumnTransform(table string, col source.Column, cc config.ColumnConfig, hashKey []byte) error {
	if !transform.Known(cc.Transform) {
		return fmt.Errorf("table %q column %q: unknown transform %q", table, col.Name, cc.Transform)
	}
	if transform.IsTextOnly(cc.Transform) && !typemap.IsText(col.DataType) {
		return fmt.Errorf("table %q column %q: transform %q requires a text column, but %q is %s",
			table, col.Name, cc.Transform, col.Name, col.DataType)
	}
	if transform.RequiresHashKey(cc.Transform) && len(hashKey) == 0 {
		return fmt.Errorf("table %q column %q: transform %q requires hashing.key to be set",
			table, col.Name, cc.Transform)
	}
	if cc.Transform == transform.Null && !col.Nullable {
		return fmt.Errorf("table %q column %q: 'null' transform cannot target NOT NULL column",
			table, col.Name)
	}
	return nil
}

func specFromConfig(cc config.ColumnConfig) transform.Spec {
	return transform.Spec{
		Transform: cc.Transform,
		Value:     cc.Value,
		KeepFirst: cc.KeepFirst,
		KeepLast:  cc.KeepLast,
		MaskChar:  cc.MaskChar,
		Group:     cc.Group,
		Length:    cc.Length,
	}
}
