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

// Prepared is a validated restore, ready to emit table by table. It loads and
// validates the manifest and any reconciliation rules once, then serves both
// the SQL-script output (Run) and live application against a database (the
// sync command, via internal/target). It holds no open files or connections.
type Prepared struct {
	// Manifest is the loaded export manifest.
	Manifest *manifest.Manifest
	// Dialect is the resolved target SQL engine.
	Dialect Dialect

	runDir    string
	rules     *Rules
	batchSize int
	log       *slog.Logger
}

// Prepare loads and validates the export run in opts.RunDir (and any
// reconciliation rules) without emitting anything. It performs every check Run
// does up front — manifest version, completeness, and rule validation — so a
// caller that applies the restore fails before touching a database.
func Prepare(opts Options) (*Prepared, error) {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.Dialect == "" {
		return nil, fmt.Errorf("dialect is required")
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	man, err := manifest.Load(filepath.Join(opts.RunDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	if man.Version > manifest.Version {
		return nil, fmt.Errorf("manifest version %d is newer than this binary understands (%d); upgrade digestive",
			man.Version, manifest.Version)
	}
	if !man.Complete && !opts.AllowIncomplete {
		return nil, fmt.Errorf("manifest reports an incomplete export (complete=false); " +
			"it may be missing rows or whole tables — pass --allow-incomplete to restore it anyway")
	}
	if !man.Complete {
		log.Warn("restoring an incomplete export", "run", man.RunID)
	}

	var rules *Rules
	if opts.RulesPath != "" {
		rules, err = LoadRules(opts.RulesPath)
		if err != nil {
			return nil, err
		}
		if err := rules.validate(man); err != nil {
			return nil, err
		}
		// Announce reconciliation on stderr: auto-discovery means the same
		// command produces different SQL depending on which directory it runs
		// in, so the rewrite must never be invisible.
		log.Info("applying restore rules", "path", opts.RulesPath, "tables", len(rules.Tables))
	}

	return &Prepared{
		Manifest:  man,
		Dialect:   opts.Dialect,
		runDir:    opts.RunDir,
		rules:     rules,
		batchSize: batchSize,
		log:       log,
	}, nil
}

// SessionStatements returns the session-setup statements to run before any
// table, excluding transaction control (the caller manages the transaction).
func (p *Prepared) SessionStatements() []string {
	return p.Dialect.sessionStatements()
}

// Tables returns the manifest tables in emit order. Tables dropped by a
// restore rule are still included; check TableRules(name).DropTable to skip
// them, exactly as Run does.
func (p *Prepared) Tables() []manifest.Table {
	return p.Manifest.Tables
}

// TableRules returns the reconciliation rules for a table, or a zero-value
// (no-op) TableRules when none are declared.
func (p *Prepared) TableRules(name string) TableRules {
	return p.rules.forTable(name)
}

// TableStat summarises one table's emitted output without generating any SQL.
// Statements is the number of multi-row INSERTs the table would produce; it is
// zero for an empty table and for a dropped one.
type TableStat struct {
	Name       string `json:"name"`
	Rows       int64  `json:"rows"`
	Statements int    `json:"statements"`
	Dropped    bool   `json:"dropped,omitempty"`
}

// Summary reports, per table, how many rows and INSERT statements a restore
// would emit — without reading the Parquet files or generating any SQL. Row
// counts come from the manifest; statement counts derive from the batch size.
// Tables dropped by a restore rule are marked Dropped and contribute nothing.
func (p *Prepared) Summary() []TableStat {
	stats := make([]TableStat, 0, len(p.Manifest.Tables))
	for _, t := range p.Manifest.Tables {
		if p.TableRules(t.Name).DropTable {
			stats = append(stats, TableStat{Name: t.Name, Rows: t.Rows, Dropped: true})
			continue
		}
		statements := 0
		if t.Rows > 0 {
			statements = int((t.Rows + int64(p.batchSize) - 1) / int64(p.batchSize))
		}
		stats = append(stats, TableStat{Name: t.Name, Rows: t.Rows, Statements: statements})
	}
	return stats
}

// Run reads the export run in opts.RunDir and streams a single SQL script to
// opts.Out.
func Run(opts Options) error {
	p, err := Prepare(opts)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(opts.Out)

	writeHeader(w, p.Manifest, p.Dialect)
	for _, stmt := range p.Dialect.preamble() {
		fmt.Fprintln(w, stmt)
	}
	fmt.Fprintln(w)

	for _, table := range p.Tables() {
		tr := p.TableRules(table.Name)
		if tr.DropTable {
			p.log.Info("skipping table per restore rules", "table", table.Name)
			continue
		}
		if _, err := p.writeTable(w, table, tr); err != nil {
			return fmt.Errorf("restore table %q: %w", table.Name, err)
		}
	}

	fmt.Fprintln(w, "COMMIT;")
	return w.Flush()
}

// WriteTable writes one table's comment header and INSERT statements to w and
// returns the number of rows emitted. It is the per-table seam sync uses to
// execute a table's INSERTs in isolation; a zero return means the table
// produced no INSERT (an empty table), so the caller can skip executing it.
// Dropped tables (TableRules.DropTable) must be filtered by the caller.
func (p *Prepared) WriteTable(w io.Writer, table manifest.Table, tr TableRules) (int64, error) {
	bw := bufio.NewWriter(w)
	rows, err := p.writeTable(bw, table, tr)
	if err != nil {
		return rows, err
	}
	return rows, bw.Flush()
}

func writeHeader(w io.Writer, man *manifest.Manifest, d Dialect) {
	fmt.Fprintf(w, "-- digestive restore — run %s, exported %s, dialect %s\n", man.RunID, man.CreatedAt, d)
	fmt.Fprintf(w, "-- source engine: %s\n\n", man.Source.Engine)
}

// writeTable writes one table's comment header and INSERT statements, applying
// the table's reconciliation rules (renames, drops, and added constant columns)
// to the emitted column list and values. It returns the number of rows emitted.
func (p *Prepared) writeTable(w *bufio.Writer, table manifest.Table, tr TableRules) (int64, error) {
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
		return 0, fmt.Errorf("no columns recorded in manifest")
	}

	path := filepath.Join(p.runDir, table.File)
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open parquet file %s: %w", table.File, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		return 0, fmt.Errorf("read parquet %s: %w", table.File, err)
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
			return 0, fmt.Errorf("column %q from manifest is absent from parquet file %s", c.Name, table.File)
		}
		k, err := kindFor(c.ParquetType)
		if err != nil {
			return 0, fmt.Errorf("column %q: %w", c.Name, err)
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

	written, err := streamRows(w, pf, len(schema.Columns()), colIndex, kinds, addLits, insertPrefix, p.batchSize)
	if err != nil {
		return written, err
	}
	if written != table.Rows {
		p.log.Warn("row count differs between parquet file and manifest",
			"table", table.Name, "parquet_rows", written, "manifest_rows", table.Rows)
	}
	fmt.Fprintln(w)
	return written, nil
}

// streamRows reads every row group of pf and writes batched multi-row INSERT
// statements. It returns the number of rows emitted. A table with no rows
// produces no INSERT at all. addLits are constant literals for added columns,
// appended verbatim after the parquet-derived values on every row.
func streamRows(w *bufio.Writer, pf *parquet.File, parquetCols int, colIndex []int, kinds []renderKind, addLits []string, insertPrefix string, batchSize int) (int64, error) {
	numCols := len(colIndex)
	// byCol is indexed by parquet leaf column index, which is independent of the
	// number of selected columns: dropped columns leave gaps, so colIndex entries
	// (and v.Column() values) can exceed numCols. Size it to the full schema width.
	byCol := make([]parquet.Value, parquetCols)
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
					if ci := v.Column(); ci >= 0 && ci < parquetCols {
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
