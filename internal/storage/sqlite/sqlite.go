// Package sqlite implements storage.Store on an embedded SQLite database.
//
// This is the zero-configuration backend: a single file under the user's data
// directory, no server to install. It is the right choice for one operator on
// one machine. Teams should use the Postgres backend, because SQLite serialises
// writers and does not survive being shared over a network filesystem.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
	"github.com/rohankewalramani/inventory-sys/internal/storage/migrate"
	sqlitedrv "modernc.org/sqlite"
)

// timeFormat is the on-disk representation for every timestamp column. UTC
// RFC3339 with nanoseconds sorts lexicographically in the same order it sorts
// chronologically, which is what lets ORDER BY on a TEXT column be correct.
const timeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// dbtx is the subset of *sql.DB and *sql.Tx the repositories use, so the same
// repository code serves both.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store is the SQLite implementation of storage.Store.
type Store struct {
	// SQLite permits one writer at a time. Rather than letting the pool
	// discover that through SQLITE_BUSY retries, writes go through a pool
	// capped at a single connection and reads go through a wider one. WAL mode
	// lets those readers proceed while the writer holds its lock.
	writeDB *sql.DB
	readDB  *sql.DB

	// tx is non-nil when this Store is bound to a transaction, in which case
	// both reads and writes must use it to see uncommitted rows.
	tx *sql.Tx
}

// Options configure Open.
type Options struct {
	// Path is the database file. ":memory:" is accepted for tests.
	Path string
	// SkipMigrate leaves the schema untouched. The desktop app migrates on
	// open; tooling that only inspects a database should not.
	SkipMigrate bool
}

// Open connects to the database at path, applying pending migrations unless
// told not to.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Path == "" {
		return nil, errors.New("sqlite: no database path given")
	}

	writeDB, err := openPool(opts.Path, 1)
	if err != nil {
		return nil, err
	}
	readDB, err := openPool(opts.Path, max(4, runtime.NumCPU()))
	if err != nil {
		_ = writeDB.Close()
		return nil, err
	}

	s := &Store{writeDB: writeDB, readDB: readDB}

	if err := s.Ping(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	if !opts.SkipMigrate {
		if _, err := migrate.Apply(ctx, writeDB, migrate.DialectSQLite); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}

// OpenMemory opens a private in-memory database, used by tests.
func OpenMemory(ctx context.Context) (*Store, error) {
	// A shared cache and a name keep every pooled connection pointed at the
	// same in-memory database; without it each connection would get its own.
	return Open(ctx, Options{Path: "file:memdb" + core.NewID().String() + "?mode=memory&cache=shared"})
}

func openPool(path string, maxConns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", withPragmas(path))
	if err != nil {
		return nil, fmt.Errorf("sqlite: opening %s: %w", path, err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	// SQLite connections are cheap but not free; recycling them bounds the
	// damage from any single connection ending up in a bad state.
	db.SetConnMaxLifetime(time.Hour)
	return db, nil
}

// withPragmas appends the connection settings this application depends on.
//
//   - WAL lets readers run concurrently with the writer.
//   - busy_timeout waits instead of failing when the writer lock is held.
//   - foreign_keys is OFF by default in SQLite, which would silently disable
//     every REFERENCES clause in the schema.
//   - synchronous=NORMAL is the documented safe pairing with WAL and avoids an
//     fsync per commit, which matters for bulk import.
func withPragmas(path string) string {
	pragmas := []string{
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(ON)",
		"_pragma=synchronous(NORMAL)",
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	if !strings.HasPrefix(path, "file:") {
		path = "file:" + url.PathEscape(path)
		if strings.Contains(path, "?") {
			sep = "&"
		} else {
			sep = "?"
		}
	}
	return path + sep + strings.Join(pragmas, "&")
}

func (s *Store) Dialect() string { return migrate.DialectSQLite }

func (s *Store) Ping(ctx context.Context) error {
	if err := s.readDB.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	var errs []error
	if s.readDB != nil {
		errs = append(errs, s.readDB.Close())
	}
	if s.writeDB != nil {
		errs = append(errs, s.writeDB.Close())
	}
	return errors.Join(errs...)
}

// read returns the handle for queries.
func (s *Store) read() dbtx {
	if s.tx != nil {
		return s.tx
	}
	return s.readDB
}

// write returns the handle for statements that modify data.
func (s *Store) write() dbtx {
	if s.tx != nil {
		return s.tx
	}
	return s.writeDB
}

// InTx runs fn inside a transaction, joining an enclosing one if present.
func (s *Store) InTx(ctx context.Context, fn func(storage.Store) error) error {
	if s.tx != nil {
		return fn(s)
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin transaction: %w", err)
	}

	txStore := &Store{writeDB: s.writeDB, readDB: s.readDB, tx: tx}
	if err := fn(txStore); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, fmt.Errorf("sqlite: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}

func (s *Store) Products() storage.ProductRepo   { return &productRepo{s} }
func (s *Store) Movements() storage.MovementRepo { return &movementRepo{s} }
func (s *Store) Locations() storage.LocationRepo { return &locationRepo{s} }
func (s *Store) Settings() storage.SettingsRepo  { return &settingsRepo{s} }
func (s *Store) Customers() storage.CustomerRepo { return &customerRepo{s} }
func (s *Store) Stores() storage.StoreRepo       { return &storeRepo{s} }
func (s *Store) Programs() storage.ProgramRepo   { return &programRepo{s} }
func (s *Store) Orders() storage.OrderRepo       { return &orderRepo{s} }

// --- shared helpers ---------------------------------------------------------

func fmtTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// dateFormat is used for calendar dates such as a lot expiry, where the time
// of day is meaningless and storing one invites timezone bugs at midnight.
const dateFormat = "2006-01-02"

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(dateFormat)
}

func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(dateFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("sqlite: parsing date %q: %w", s, err)
	}
	return t, nil
}

// sortStringsFold orders strings the way a person reads a list, ignoring case.
func sortStringsFold(values []string) {
	sort.SliceStable(values, func(i, j int) bool {
		return strings.ToLower(values[i]) < strings.ToLower(values[j])
	})
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		// Tolerate timestamps written by an older build or by hand.
		if t2, err2 := time.Parse(time.RFC3339Nano, s); err2 == nil {
			return t2.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("sqlite: parsing timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// SQLite extended result codes we act on.
const (
	codeConstraintPrimaryKey = 1555
	codeConstraintUnique     = 2067
	codeConstraintForeignKey = 787
	codeConstraintTrigger    = 1811
	codeConstraintCheck      = 275
)

// mapError translates a driver error into one of the core sentinels so callers
// never have to know which backend produced it.
func mapError(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, core.ErrNotFound)
	}

	var serr *sqlitedrv.Error
	if errors.As(err, &serr) {
		switch serr.Code() {
		case codeConstraintUnique, codeConstraintPrimaryKey:
			return fmt.Errorf("%s: %w: a record with that identifier already exists", op, core.ErrConflict)
		case codeConstraintForeignKey:
			return fmt.Errorf("%s: %w: the record is still referenced by other data", op, core.ErrConflict)
		case codeConstraintTrigger:
			return fmt.Errorf("%s: %w: %s", op, core.ErrConflict, serr.Error())
		case codeConstraintCheck:
			return fmt.Errorf("%s: %w: %s", op, core.ErrInvalid, serr.Error())
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// escapeLike neutralises the wildcards in a user's search term so that typing
// "50%" searches for the literal text rather than matching everything.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// direction renders a validated sort direction. Anything unrecognised falls
// back to ascending rather than reaching SQL.
func direction(d storage.SortDirection) string {
	if d == storage.Descending {
		return "DESC"
	}
	return "ASC"
}

// limitClause renders LIMIT/OFFSET, omitting them when unpaged.
func limitClause(limit, offset int, args *[]any) string {
	switch {
	case limit > 0 && offset > 0:
		*args = append(*args, limit, offset)
		return " LIMIT ? OFFSET ?"
	case limit > 0:
		*args = append(*args, limit)
		return " LIMIT ?"
	case offset > 0:
		// SQLite requires a LIMIT before OFFSET; -1 means unbounded.
		*args = append(*args, offset)
		return " LIMIT -1 OFFSET ?"
	}
	return ""
}

// requireOneRow turns a zero-row write into the right sentinel.
//
// A write that matched nothing means either the record is gone or somebody
// else saved first, and telling those apart is what lets the UI say which. The
// optional exists check distinguishes them; without one, a miss is a plain
// not-found.
func requireOneRow(res sql.Result, exists func() error, op string, id core.ID) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s %s: %w", op, id, err)
	}
	if n > 0 {
		return nil
	}

	if exists != nil {
		if err := exists(); err != nil {
			return err
		}
		return fmt.Errorf("%s %s: %w: the record was changed by someone else since you opened it",
			op, id, core.ErrConflict)
	}
	return fmt.Errorf("%s %s: %w", op, id, core.ErrNotFound)
}
