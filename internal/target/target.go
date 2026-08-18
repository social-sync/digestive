// Package target applies a prepared restore straight into a live destination
// database. Where the restore package emits a SQL script for a human to pipe
// into a client, target opens the destination itself and executes that same
// SQL over the Go SQL driver, inside a single transaction, so `digestive sync`
// is a connection-driven end-to-end export→apply.
//
// The destination `type` (from config) selects both the driver and the restore
// dialect. Every type supported today speaks the MySQL wire protocol, so they
// share the go-sql-driver/mysql driver and differ only in dialect; the resolve
// seam is where a future non-MySQL engine (e.g. Postgres) would register its
// own driver.
package target

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/social-sync/digestive/internal/restore"
)

// Type is the destination engine named by config `sync.type`.
type Type string

const (
	// MySQL applies with the MySQL dialect (constraint-check toggles on).
	MySQL Type = "mysql"
	// SingleStore applies with the SingleStore dialect over the MySQL driver.
	SingleStore Type = "singlestore"
)

// binding is the driver + restore dialect a Type resolves to.
type binding struct {
	driver  string
	dialect restore.Dialect
}

// resolve maps a config `type` to its driver and dialect, or returns a clear
// error naming the supported values. It is the single place new engines slot
// in.
func resolve(t string) (binding, error) {
	switch Type(t) {
	case MySQL:
		return binding{driver: "mysql", dialect: restore.MySQL}, nil
	case SingleStore:
		return binding{driver: "mysql", dialect: restore.SingleStore}, nil
	case "":
		return binding{}, fmt.Errorf("sync.type is required (supported: %s, %s)", MySQL, SingleStore)
	default:
		return binding{}, fmt.Errorf("unknown sync.type %q (supported: %s, %s)", t, MySQL, SingleStore)
	}
}

// Target is an opened destination database ready to apply a restore into.
type Target struct {
	db      *sql.DB
	dialect restore.Dialect
	host    string
	name    string
}

// Open resolves typ, forces multi-statement execution on the DSN (a restore
// emits several statements per table), and opens a pooled connection. It does
// not dial the server; call Ping to verify connectivity.
func Open(typ, dsn string) (*Target, error) {
	b, err := resolve(typ)
	if err != nil {
		return nil, err
	}
	if dsn == "" {
		return nil, fmt.Errorf("sync.dsn is required")
	}

	// Every supported type is MySQL-wire, so the DSN is parsed with the mysql
	// driver both to force multiStatements (restore batches many INSERTs and
	// SET statements into one execution) and to surface the host and database
	// for the sync confirmation guard.
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse sync.dsn: %w", err)
	}
	cfg.MultiStatements = true

	db, err := sql.Open(b.driver, cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open destination: %w", err)
	}
	return &Target{db: db, dialect: b.dialect, host: cfg.Addr, name: cfg.DBName}, nil
}

// Dialect is the restore dialect this destination's type resolved to. It is the
// default dialect for a sync; a --dialect flag may override it.
func (t *Target) Dialect() restore.Dialect { return t.dialect }

// Host is the destination address (host:port) parsed from the DSN.
func (t *Target) Host() string { return t.host }

// Database is the destination database name parsed from the DSN.
func (t *Target) Database() string { return t.name }

// Ping verifies the destination is reachable.
func (t *Target) Ping(ctx context.Context) error { return t.db.PingContext(ctx) }

// Close releases the connection pool.
func (t *Target) Close() error { return t.db.Close() }

// Reporter receives per-table progress during Apply. A nil Reporter discards
// every event.
type Reporter interface {
	// TableStart fires before a table's INSERTs are executed.
	TableStart(table string)
	// TableDone fires after a table's INSERTs commit within the transaction.
	TableDone(table string, rows int64)
}

type nopReporter struct{}

func (nopReporter) TableStart(string)       {}
func (nopReporter) TableDone(string, int64) {}

// Apply executes the prepared restore against the destination inside a single
// transaction: any error rolls the whole thing back, leaving the destination
// untouched, and success commits atomically. The SQL executed is byte-for-byte
// what restore would have emitted (minus its self-contained transaction, which
// this method manages), so a sync and a piped restore load identical data.
//
// The destination schema must already exist — a restore is INSERTs only, so a
// missing table surfaces here as a database error and rolls the sync back.
func (t *Target) Apply(ctx context.Context, p *restore.Prepared, rep Reporter) error {
	if rep == nil {
		rep = nopReporter{}
	}

	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Roll back on any early return. After a successful Commit the rollback is a
	// harmless no-op (sql.ErrTxDone), so this stays correct on the happy path.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, stmt := range p.SessionStatements() {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("session setup (%s): %w", stmt, err)
		}
	}

	for _, table := range p.Tables() {
		tr := p.TableRules(table.Name)
		if tr.DropTable {
			continue
		}

		var buf bytes.Buffer
		rows, err := p.WriteTable(&buf, table, tr)
		if err != nil {
			return fmt.Errorf("build table %q: %w", table.Name, err)
		}
		// An empty table emits only a comment; executing that alone errors on
		// some servers ("query was empty"), so skip it.
		if rows == 0 {
			continue
		}

		rep.TableStart(table.Name)
		if _, err := tx.ExecContext(ctx, buf.String()); err != nil {
			return fmt.Errorf("apply table %q: %w", table.Name, err)
		}
		rep.TableDone(table.Name, rows)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}
