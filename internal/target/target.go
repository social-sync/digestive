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

// defaultMaxPacketBytes bounds how many bytes of a table's INSERT SQL are sent
// to the destination in a single ExecContext. A table's statements are split on
// their boundaries into chunks no larger than this, so a large table no longer
// overflows the driver/server max_allowed_packet limit. 4 MiB is the most
// conservative common server default (older MySQL), so it works everywhere out
// of the box; raise it via sync.max_packet_bytes when the destination allows
// bigger packets and fewer round trips are wanted.
const defaultMaxPacketBytes = 4 << 20

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
	db        *sql.DB
	dialect   restore.Dialect
	host      string
	name      string
	maxPacket int
}

// Option configures a Target at open time.
type Option func(*Target)

// WithMaxPacketBytes sets the maximum byte size of a single statement batch sent
// to the destination during Apply. A table's INSERTs are split into chunks no
// larger than this so a large table never overflows the driver/server
// max_allowed_packet limit. A non-positive value keeps the default.
func WithMaxPacketBytes(n int) Option {
	return func(t *Target) {
		if n > 0 {
			t.maxPacket = n
		}
	}
}

// Open resolves typ, forces multi-statement execution on the DSN (a restore
// emits several statements per table), and opens a pooled connection. It does
// not dial the server; call Ping to verify connectivity.
func Open(typ, dsn string, opts ...Option) (*Target, error) {
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
	t := &Target{db: db, dialect: b.dialect, host: cfg.Addr, name: cfg.DBName, maxPacket: defaultMaxPacketBytes}
	for _, o := range opts {
		o(t)
	}
	return t, nil
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
//
// A table's INSERTs are executed in packet-sized chunks (see WithMaxPacketBytes)
// rather than one giant statement, so a large table never overflows the
// destination's max_allowed_packet limit. Every chunk still runs inside the one
// transaction, so the all-or-nothing guarantee is unchanged.
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
		for _, chunk := range packetChunks(buf.Bytes(), t.maxPacket) {
			if _, err := tx.ExecContext(ctx, string(chunk)); err != nil {
				return fmt.Errorf("apply table %q: %w", table.Name, err)
			}
		}
		rep.TableDone(table.Name, rows)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// packetChunks splits a table's INSERT SQL into slices to execute in order, each
// grouping whole statements into no more than maxPacket bytes. Executing each
// chunk separately keeps every packet under the destination's max_allowed_packet
// limit while preserving MultiStatements batching within a chunk.
//
// Statements are split on the ";\n" that restore writes after every statement;
// restore escapes newlines inside string values (see restore.quoteString), so
// that sequence only ever marks a real statement boundary. A single statement
// larger than maxPacket cannot be split and is returned on its own — lower
// sync.batch_size if even one statement overflows the limit. A maxPacket <= 0
// returns the whole table as one chunk (the pre-chunking behaviour). A chunk
// that is only whitespace (such as the trailing blank line restore appends) is
// dropped so no empty query is executed; Apply already skips zero-row tables, so
// a header-only chunk never reaches here.
func packetChunks(sqlBytes []byte, maxPacket int) [][]byte {
	nonEmpty := func(chunks [][]byte, chunk []byte) [][]byte {
		if len(bytes.TrimSpace(chunk)) == 0 {
			return chunks
		}
		return append(chunks, chunk)
	}

	if maxPacket <= 0 {
		return nonEmpty(nil, sqlBytes)
	}

	sep := []byte(";\n")
	var chunks [][]byte
	// chunkStart marks the first byte of the statements queued but not yet
	// emitted; pos walks statement by statement.
	chunkStart, pos := 0, 0
	for pos < len(sqlBytes) {
		i := bytes.Index(sqlBytes[pos:], sep)
		stmtEnd := len(sqlBytes)
		if i >= 0 {
			stmtEnd = pos + i + len(sep)
		}
		// If adding this statement would exceed the budget and something is
		// already queued, emit the queued statements so this one starts fresh.
		if stmtEnd-chunkStart > maxPacket && pos > chunkStart {
			chunks = nonEmpty(chunks, sqlBytes[chunkStart:pos])
			chunkStart = pos
		}
		pos = stmtEnd
		if i < 0 {
			break
		}
	}
	return nonEmpty(chunks, sqlBytes[chunkStart:])
}
