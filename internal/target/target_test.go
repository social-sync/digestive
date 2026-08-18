package target

import (
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
