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

	"github.com/social-sync/grimnir/internal/manifest"
	"github.com/parquet-go/parquet-go"
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
		return fmt.Errorf("manifest version %d is newer than this binary understands (%d); upgrade grimnir",
			man.Version, manifest.Version)
	}
	if !man.Complete && !opts.AllowIncomplete {
		return fmt.Errorf("manifest reports an incomplete export (complete=false); " +
			"it may be missing rows or whole tables — pass --allow-incomplete to restore it anyway")
	}
	if !man.Complete {
		log.Warn("restoring an incomplete export", "run", man.RunID)
	}

	w := bufio.NewWriter(opts.Out)

	writeHeader(w, man, opts.Dialect)
	for _, stmt := range opts.Dialect.preamble() {
		fmt.Fprintln(w, stmt)
	}
	fmt.Fprintln(w)

	for _, table := range man.Tables {
		if err := restoreTable(w, opts.RunDir, table, batchSize, log); err != nil {
			return fmt.Errorf("restore table %q: %w", table.Name, err)
		}
	}

	fmt.Fprintln(w, "COMMIT;")
	return w.Flush()
}

func writeHeader(w io.Writer, man *manifest.Manifest, d Dialect) {
	fmt.Fprintf(w, "-- grimnir restore — run %s, exported %s, dialect %s\n", man.RunID, man.CreatedAt, d)
	fmt.Fprintf(w, "-- source engine: %s\n\n", man.Source.Engine)
}

// restoreTable writes one table's comment header and INSERT statements.
func restoreTable(w *bufio.Writer, runDir string, table manifest.Table, batchSize int, log *slog.Logger) error {
	fmt.Fprintf(w, "-- table: %s (%d rows)\n", table.Name, table.Rows)

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

	// Resolve each manifest column to its Parquet leaf index (the schema
	// orders columns independently of the manifest) and its render kind.
	colIndex := make([]int, len(table.Columns))
	kinds := make([]renderKind, len(table.Columns))
	quotedCols := make([]string, len(table.Columns))
	for i, c := range table.Columns {
		leaf, ok := schema.Lookup(c.Name)
		if !ok {
			return fmt.Errorf("column %q from manifest is absent from parquet file %s", c.Name, table.File)
		}
		colIndex[i] = leaf.ColumnIndex
		k, err := kindFor(c.ParquetType)
		if err != nil {
			return fmt.Errorf("column %q: %w", c.Name, err)
		}
		kinds[i] = k
		quotedCols[i] = quoteIdent(c.Name)
	}

	insertPrefix := fmt.Sprintf("INSERT INTO %s (%s) VALUES\n",
		quoteIdent(table.Name), strings.Join(quotedCols, ", "))

	written, err := streamRows(w, pf, colIndex, kinds, insertPrefix, batchSize)
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
// produces no INSERT at all.
func streamRows(w *bufio.Writer, pf *parquet.File, colIndex []int, kinds []renderKind, insertPrefix string, batchSize int) (int64, error) {
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
				for k := 0; k < numCols; k++ {
					if k > 0 {
						w.WriteString(", ")
					}
					lit, err := renderValue(byCol[colIndex[k]], kinds[k])
					if err != nil {
						rows.Close()
						return total, err
					}
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
