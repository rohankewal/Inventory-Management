package sqlite_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// rawDB opens the database bypassing the Store, so a test can attempt writes
// the application layer would never issue.
func rawDB(t *testing.T, path string) (*sql.DB, error) {
	t.Helper()
	return sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
}
