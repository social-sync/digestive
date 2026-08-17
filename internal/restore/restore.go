// Package restore turns an export run (a manifest.json plus one Parquet file
// per table) into a single SQL script of INSERT statements that loads into a
// copy of the source database. It connects to nothing: the manifest and the
// Parquet files are the sole inputs, and the manifest's recorded source types
// drive how each value becomes a SQL literal.
package restore

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/social-sync/digestive/internal/manifest"
)

// defaultBatchSize is the number of rows per multi-row INSERT statement.
const defaultBatchSize = 1000

// Options configures a restore run.
type Options struct {
	// RunDir is an export run directory: a manifest.json and one Parquet file
	// per table.
	RunDir string
	// Dialect selects the target SQL engine. Required; it currently affects
	// only the session preamble.
	Dialect Dialect
	// BatchSize is the number of rows per INSERT statement (default 1000).
	BatchSize int
	// AllowIncomplete permits restoring a run whose manifest reports
	// complete=false.
	AllowIncomplete bool
	// RulesPath is an optional path to a restore.yaml of schema-reconciliation
	// rules (rename/drop/add columns, rename/drop tables) applied to the
	// emitted SQL. Empty means no reconciliation: behaviour is identical to
	// reading only the run directory.
	RulesPath string
	// Out receives the SQL script.
	Out io.Writer
	// Logger receives progress and warnings; defaults to a no-op logger.
	Logger *slog.Logger
}

// Run reads the export run in opts.RunDir and streams a single SQL script to
// opts.Out.
func Run(opts Options) error {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.Dialect == "" {
		return fmt.Errorf("dialect is required")
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	man, err := manifest.Load(filepath.Join(opts.RunDir, "manifest.json"))
	if err != nil {
		return err
	}
	if man.Version > manifest.Version {
		return fmt.Errorf("manifest version %d is newer than this binary understands (%d); upgrade digestive",
			man.Version, manifest.Version)
	}
	if !man.Complete && !opts.AllowIncomplete {
		return fmt.Errorf("manifest reports an incomplete export (complete=false); " +
			"it may be missing rows or whole tables — pass --allow-incomplete to restore it anyway")
	}
	if !man.Complete {
		log.Warn("restoring an incomplete export", "run", man.RunID)
	}

	var rules *Rules
	if opts.RulesPath != "" {
		rules, err = LoadRules(opts.RulesPath)
		if err != nil {
			return err
		}
		if err := rules.validate(man); err != nil {
			return err
		}
		// Announce reconciliation on stderr: auto-discovery means the same
		// command produces different SQL depending on which directory it runs
		// in, so the rewrite must never be invisible.
		log.Info("applying restore rules", "path", opts.RulesPath, "tables", len(rules.Tables))
	}

	w := bufio.NewWriter(opts.Out)

	writeHeader(w, man, opts.Dialect)
	for _, stmt := range opts.Dialect.preamble() {
		fmt.Fprintln(w, stmt)
	}
	fmt.Fprintln(w)

	for _, table := range man.Tables {
		tr := rules.forTable(table.Name)
		if tr.DropTable {
			log.Info("skipping table per restore rules", "table", table.Name)
			continue
		}
		if err := restoreTable(w, opts.RunDir, table, tr, batchSize, log); err != nil {
			return fmt.Errorf("restore table %q: %w", table.Name, err)
		}
	}

	fmt.Fprintln(w, "COMMIT;")
	return w.Flush()
}

func writeHeader(w io.Writer, man *manifest.Manifest, d Dialect) {
	fmt.Fprintf(w, "-- digestive restore — run %s, exported %s, dialect %s\n", man.RunID, man.CreatedAt, d)
	fmt.Fprintf(w, "-- source engine: %s\n\n", man.Source.Engine)
}

// restoreTable writes one table's comment header and INSERT statements,
// applying the table's reconciliation rules (renames, drops, and added
// constant columns) to the emitted column list and values.
func restoreTable(w *bufio.Writer, runDir string, table manifest.Table, tr TableRules, batchSize int, log *slog.Logger) error {
	emitTable := table.Name
	if tr.RenameTable != "" {
		emitTable = tr.RenameTable
	}
	if emitTable == table.Name {
		fmt.Fprintf(w, "-- table: %s (%d rows)\n", table.Name, table.Rows)
	} else {
		fmt.Fprintf(w, "-- table: %s -> %s (%d rows)\n", table.Name, emitTable, table.Rows)
	}

	if len(table.Columns) == 0 {
		return fmt.Errorf("no columns recorded in manifest")
	}

	path := filepath.Join(runDir, table.File)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open parquet file %s: %w", table.File, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return fmt.Errorf("read parquet %s: %w", table.File, err)
	}
	schema := pf.Schema()

	dropped := make(map[string]bool, len(tr.DropColumns))
	for _, c := range tr.DropColumns {
		dropped[c] = true
	}

	// Resolve each surviving manifest column to its Parquet leaf index (the
	// schema orders columns independently of the manifest), its render kind,
	// and its emitted (possibly renamed) name. Dropped columns are skipped.
	var colIndex []int
	var kinds []renderKind
	var emitCols []string
	for _, c := range table.Columns {
		if dropped[c.Name] {
			continue
		}
		leaf, ok := schema.Lookup(c.Name)
		if !ok {
			return fmt.Errorf("column %q from manifest is absent from parquet file %s", c.Name, table.File)
		}
		k, err := kindFor(c.ParquetType)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
		emit := c.Name
		if dst, ok := tr.RenameColumns[c.Name]; ok {
			emit = dst
		}
		colIndex = append(colIndex, leaf.ColumnIndex)
		kinds = append(kinds, k)
		emitCols = append(emitCols, quoteIdent(emit))
	}

	// Added columns carry a constant literal repeated on every row, appended in
	// sorted order for deterministic output.
	addNames := sortedKeys(tr.AddColumns)
	addLits := make([]string, len(addNames))
	for i, name := range addNames {
		addLits[i] = tr.AddColumns[name].literal()
		emitCols = append(emitCols, quoteIdent(name))
	}

	insertPrefix := fmt.Sprintf("INSERT INTO %s (%s) VALUES\n",
		quoteIdent(emitTable), strings.Join(emitCols, ", "))

	written, err := streamRows(w, pf, colIndex, kinds, addLits, insertPrefix, batchSize)
	if err != nil {
		return err
	}
	if written != table.Rows {
		log.Warn("row count differs between parquet file and manifest",
			"table", table.Name, "parquet_rows", written, "manifest_rows", table.Rows)
	}
	fmt.Fprintln(w)
	return nil
}

// streamRows reads every row group of pf and writes batched multi-row INSERT
// statements. It returns the number of rows emitted. A table with no rows
// produces no INSERT at all. addLits are constant literals for added columns,
// appended verbatim after the parquet-derived values on every row.
func streamRows(w *bufio.Writer, pf *parquet.File, colIndex []int, kinds []renderKind, addLits []string, insertPrefix string, batchSize int) (int64, error) {
	numCols := len(colIndex)
	byCol := make([]parquet.Value, numCols)
	buf := make([]parquet.Row, 256)

	var total int64
	inBatch := 0 // rows accumulated in the open INSERT statement, 0 when none

	for _, rg := range pf.RowGroups() {
		rows := rg.Rows()
		for {
			n, readErr := rows.ReadRows(buf)
			for i := 0; i < n; i++ {
				for j := range byCol {
					byCol[j] = parquet.Value{}
				}
				for _, v := range buf[i] {
					if ci := v.Column(); ci >= 0 && ci < numCols {
						byCol[ci] = v
					}
				}

				if inBatch == 0 {
					w.WriteString(insertPrefix)
				} else {
					w.WriteString(",\n")
				}
				w.WriteByte('(')
				first := true
				for k := 0; k < numCols; k++ {
					if !first {
						w.WriteString(", ")
					}
					first = false
					lit, err := renderValue(byCol[colIndex[k]], kinds[k])
					if err != nil {
						rows.Close()
						return total, err
					}
					w.WriteString(lit)
				}
				for _, lit := range addLits {
					if !first {
						w.WriteString(", ")
					}
					first = false
					w.WriteString(lit)
				}
				w.WriteByte(')')

				inBatch++
				total++
				if inBatch >= batchSize {
					w.WriteString(";\n")
					inBatch = 0
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				rows.Close()
				return total, fmt.Errorf("read rows: %w", readErr)
			}
		}
		rows.Close()
	}
	if inBatch > 0 {
		w.WriteString(";\n")
	}
	return total, nil
}
