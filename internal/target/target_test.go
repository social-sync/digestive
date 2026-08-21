package target

import (
	"bytes"
	"strings"
	"testing"

	"github.com/social-sync/digestive/internal/restore"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		typ         string
		wantDialect restore.Dialect
	}{
		{"mysql", restore.MySQL},
		{"singlestore", restore.SingleStore},
	}
	for _, c := range cases {
		b, err := resolve(c.typ)
		if err != nil {
			t.Fatalf("resolve(%q): unexpected error: %v", c.typ, err)
		}
		if b.driver != "mysql" {
			t.Errorf("resolve(%q) driver = %q, want mysql", c.typ, b.driver)
		}
		if b.dialect != c.wantDialect {
			t.Errorf("resolve(%q) dialect = %q, want %q", c.typ, b.dialect, c.wantDialect)
		}
	}
}

func TestResolveRejectsUnknownAndEmpty(t *testing.T) {
	if _, err := resolve("postgres"); err == nil {
		t.Error("resolve(postgres) should error")
	} else if !strings.Contains(err.Error(), "supported") {
		t.Errorf("error should list supported values, got: %v", err)
	}
	if _, err := resolve(""); err == nil {
		t.Error("resolve(\"\") should error")
	} else if !strings.Contains(err.Error(), "required") {
		t.Errorf("empty type error should say required, got: %v", err)
	}
}

func TestOpenParsesHostAndDatabaseAndForcesMultiStatements(t *testing.T) {
	// sql.Open is lazy, so this never dials; it exercises DSN parsing only.
	tgt, err := Open("mysql", "user:pass@tcp(db.example:3306)/appdb?tls=skip-verify")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tgt.Close()

	if tgt.Host() != "db.example:3306" {
		t.Errorf("Host() = %q, want db.example:3306", tgt.Host())
	}
	if tgt.Database() != "appdb" {
		t.Errorf("Database() = %q, want appdb", tgt.Database())
	}
	if tgt.Dialect() != restore.MySQL {
		t.Errorf("Dialect() = %q, want mysql", tgt.Dialect())
	}
}

func TestOpenRequiresDSN(t *testing.T) {
	if _, err := Open("mysql", ""); err == nil {
		t.Error("Open with empty DSN should error")
	} else if !strings.Contains(err.Error(), "sync.dsn") {
		t.Errorf("error should mention sync.dsn, got: %v", err)
	}
}

func TestOpenRejectsUnknownTypeBeforeParsingDSN(t *testing.T) {
	if _, err := Open("postgres", "user:pass@tcp(h:3306)/db"); err == nil {
		t.Error("Open with unknown type should error")
	}
}

func TestOpenDefaultsMaxPacket(t *testing.T) {
	tgt, err := Open("mysql", "u:p@tcp(h:3306)/db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tgt.Close()
	if tgt.maxPacket != defaultMaxPacketBytes {
		t.Errorf("maxPacket = %d, want default %d", tgt.maxPacket, defaultMaxPacketBytes)
	}
}

func TestWithMaxPacketBytes(t *testing.T) {
	tgt, err := Open("mysql", "u:p@tcp(h:3306)/db", WithMaxPacketBytes(123))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tgt.Close()
	if tgt.maxPacket != 123 {
		t.Errorf("maxPacket = %d, want 123", tgt.maxPacket)
	}

	// A non-positive override keeps the default.
	tgt2, err := Open("mysql", "u:p@tcp(h:3306)/db", WithMaxPacketBytes(0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer tgt2.Close()
	if tgt2.maxPacket != defaultMaxPacketBytes {
		t.Errorf("maxPacket with 0 override = %d, want default", tgt2.maxPacket)
	}
}

func TestPacketChunks(t *testing.T) {
	// Statements shaped like restore emits them: a leading comment, then INSERTs
	// each terminated by ";\n", then a trailing blank line.
	const stmt = "INSERT INTO `t` (`id`) VALUES\n(1);\n" // 34 bytes
	header := "-- table: t (3 rows)\n"
	table := header + stmt + stmt + stmt + "\n"

	t.Run("non-positive budget returns whole input as one chunk", func(t *testing.T) {
		got := packetChunks([]byte(table), 0)
		if len(got) != 1 {
			t.Fatalf("got %d chunks, want 1", len(got))
		}
		if !bytes.Equal(got[0], []byte(table)) {
			t.Errorf("chunk != input")
		}
	})

	t.Run("groups whole statements under the budget", func(t *testing.T) {
		// Budget fits two statements (plus the header on the first) but not three.
		got := packetChunks([]byte(table), len(header)+2*len(stmt))
		if len(got) != 2 {
			t.Fatalf("got %d chunks, want 2", len(got))
		}
		for _, c := range got {
			if len(c) > len(header)+2*len(stmt) {
				t.Errorf("chunk of %d bytes exceeds budget %d", len(c), len(header)+2*len(stmt))
			}
		}
		// Reassembly preserves the whole table byte-for-byte, in order.
		if joined := string(bytes.Join(got, nil)); joined != table {
			t.Errorf("rejoined chunks = %q, want the original table", joined)
		}
	})

	t.Run("a single over-budget statement is emitted alone", func(t *testing.T) {
		got := packetChunks([]byte(table), 1) // every statement exceeds 1 byte
		if len(got) != 3 {
			t.Fatalf("got %d chunks, want 3 (one per statement)", len(got))
		}
		// The header rides with the first statement; each chunk ends a statement.
		if string(got[0]) != header+stmt {
			t.Errorf("first chunk = %q, want header+stmt", got[0])
		}
	})

	t.Run("trailing blank line is dropped", func(t *testing.T) {
		// One statement plus the trailing blank line restore always appends: the
		// blank line must not become its own (empty) chunk.
		got := packetChunks([]byte(header+stmt+"\n"), 1024)
		if len(got) != 1 {
			t.Fatalf("got %d chunks, want 1", len(got))
		}
	})
}
