//go:build integration

package inttest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/social-sync/digestive/internal/restore"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/wait"

	_ "github.com/go-sql-driver/mysql"
)

const (
	engineMySQL       = "mysql"
	engineSingleStore = "singlestore"
)

// engine is one running database under test: its name, the dialect restore
// targets, and a DSN in go-sql-driver format that source.OpenSingleStore and a
// raw *sql.DB both accept.
type engine struct {
	name    string
	dialect restore.Dialect
	dsn     string
	db      *sql.DB
}

// mysqlImage / singleStoreImage are overridable so a developer can pin a version
// (e.g. MySQL 9 to exercise the native VECTOR type).
func mysqlImage() string {
	if v := os.Getenv("DIGESTIVE_MYSQL_IMAGE"); v != "" {
		return v
	}
	return "mysql:8.4"
}

func singleStoreImage() string {
	if v := os.Getenv("DIGESTIVE_SINGLESTORE_IMAGE"); v != "" {
		return v
	}
	return "ghcr.io/singlestore-labs/singlestoredb-dev:latest"
}

// startMySQL brings up a MySQL container and returns a ready engine plus a
// cleanup func. The DSN is built from the container's mapped random host port —
// never a fixed :3306 — so a developer already running MySQL locally is not
// blocked. sql_mode is relaxed to NO_ENGINE_SUBSTITUTION so the fixtures can
// insert MySQL zero-dates ('0000-00-00'); multiStatements lets us exec the
// restore script (SET…; START TRANSACTION; INSERT…; COMMIT;) in one call.
func startMySQL(ctx context.Context, t *testing.T) (*engine, func()) {
	t.Helper()

	container, err := tcmysql.Run(ctx, mysqlImage(),
		tcmysql.WithDatabase("digestive_test"),
		tcmysql.WithUsername("root"),
		tcmysql.WithPassword(""),
	)
	if err != nil {
		t.Fatalf("start mysql container: %v", err)
	}

	dsn, err := container.ConnectionString(ctx,
		"multiStatements=true",
		"parseTime=false",
		"sql_mode=%27NO_ENGINE_SUBSTITUTION%27",
	)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("mysql connection string: %v", err)
	}

	db := openAndWait(ctx, t, dsn)
	cleanup := func() {
		db.Close()
		_ = container.Terminate(context.Background())
	}
	return &engine{name: engineMySQL, dialect: restore.MySQL, dsn: dsn, db: db}, cleanup
}

// startSingleStore brings up a SingleStore dev container. It requires a free
// license key in SINGLESTORE_LICENSE; when that (or Docker) is absent the caller
// skips the SingleStore leg — it never fails the suite for want of a key.
func startSingleStore(ctx context.Context, t *testing.T) (*engine, func()) {
	t.Helper()

	license := os.Getenv("SINGLESTORE_LICENSE")
	if license == "" {
		t.Skip("SINGLESTORE_LICENSE not set — skipping the SingleStore leg. " +
			"Get a free key at https://portal.singlestore.com and export it to run these.")
	}
	const rootPassword = "Digestive-Test1!"

	req := testcontainers.ContainerRequest{
		Image:        singleStoreImage(),
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"SINGLESTORE_LICENSE": license,
			"ROOT_PASSWORD":       rootPassword,
		},
		WaitingFor: wait.ForSQL("3306/tcp", "mysql", func(host string, port network.Port) string {
			return fmt.Sprintf("root:%s@tcp(%s:%s)/", rootPassword, host, port.Port())
		}).WithStartupTimeout(3 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start singlestore container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("singlestore host: %v", err)
	}
	mapped, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("singlestore mapped port: %v", err)
	}

	// Create the working database, then point the DSN at it.
	admin := fmt.Sprintf("root:%s@tcp(%s:%s)/?multiStatements=true", rootPassword, host, mapped.Port())
	adb := openAndWait(ctx, t, admin)
	if _, err := adb.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS digestive_test"); err != nil {
		adb.Close()
		_ = container.Terminate(ctx)
		t.Fatalf("create singlestore database: %v", err)
	}
	adb.Close()

	dsn := fmt.Sprintf("root:%s@tcp(%s:%s)/digestive_test?multiStatements=true&parseTime=false",
		rootPassword, host, mapped.Port())
	db := openAndWait(ctx, t, dsn)
	cleanup := func() {
		db.Close()
		_ = container.Terminate(context.Background())
	}
	return &engine{name: engineSingleStore, dialect: restore.SingleStore, dsn: dsn, db: db}, cleanup
}

// openAndWait opens a pool and pings until the server answers or the deadline
// passes. The module wait strategies already gate on readiness; this is a cheap
// belt-and-braces for the manually-built SingleStore DSN.
func openAndWait(ctx context.Context, t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if err := db.PingContext(ctx); err == nil {
			return db
		}
		if time.Now().After(deadline) {
			db.Close()
			t.Fatalf("database never became ready: %s", dsn)
		}
		time.Sleep(time.Second)
	}
}
