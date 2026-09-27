package database

import (
	"database/sql"
	"testing"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/testsupport/mysqltest"
)

type realMySQL struct {
	server  *mysqltest.Server
	root    string
	tlsName string
	admin   *sql.DB
}

func startRealMySQL(t *testing.T) *realMySQL {
	t.Helper()
	server := mysqltest.New(t)
	return &realMySQL{server: server, root: server.Root(), tlsName: server.TLSName(), admin: server.Admin()}
}

func (f *realMySQL) start(t *testing.T, bootstrap bool) {
	t.Helper()
	f.server.Start(t, bootstrap)
	f.admin = f.server.Admin()
}

func (f *realMySQL) stop(t *testing.T) {
	t.Helper()
	f.server.Stop(t)
}

func (f *realMySQL) dsn(user, name string) string { return f.server.DSN(user, name) }

func (f *realMySQL) database(t *testing.T) *Database {
	t.Helper()
	return openRealMySQL(t, f.server.Database(t))
}

func (f *realMySQL) open(t *testing.T, user, name string) *Database {
	t.Helper()
	return openRealMySQL(t, config.DatabaseConfig{Driver: "mysql", DSN: f.dsn(user, name), MaxOpenConns: 2, MaxIdleConns: 1})
}

func openRealMySQL(t *testing.T, cfg config.DatabaseConfig) *Database {
	t.Helper()
	db, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatal("Scert 打开隔离数据库失败")
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
