package trino

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

// Executor is the only path from the provider to Trino. Resources and data
// sources depend on this interface, never on database/sql directly.
type Executor interface {
	// Exec runs a statement that returns no rows (DDL).
	Exec(ctx context.Context, statement string, args ...any) error
	// Query runs a query and returns all rows.
	Query(ctx context.Context, statement string, args ...any) ([]Row, error)
	// QueryOne returns the first row and false when the result is empty.
	QueryOne(ctx context.Context, statement string, args ...any) (Row, bool, error)
	// Version returns the cached server version, probing once.
	Version(ctx context.Context) (string, error)
	// Close releases the pool.
	Close() error
}

var _ Executor = (*sqlExecutor)(nil)

type sqlExecutor struct {
	db *sql.DB

	versionOnce sync.Once
	version     string
	versionErr  error
}

func (e *sqlExecutor) Exec(ctx context.Context, statement string, args ...any) error {
	_, err := e.db.ExecContext(ctx, statement, args...)
	return err
}

func (e *sqlExecutor) Query(ctx context.Context, statement string, args ...any) ([]Row, error) {
	rows, err := e.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := make([]Row, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		row := make(Row, len(columns))
		for i, column := range columns {
			row[strings.ToLower(column)] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (e *sqlExecutor) QueryOne(ctx context.Context, statement string, args ...any) (Row, bool, error) {
	rows, err := e.Query(ctx, statement, args...)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows[0], true, nil
}

func (e *sqlExecutor) Version(ctx context.Context) (string, error) {
	e.versionOnce.Do(func() {
		row, ok, err := e.QueryOne(ctx, "SELECT version()")
		if err != nil {
			e.versionErr = err
			return
		}
		if !ok {
			e.versionErr = fmt.Errorf("version probe returned no rows")
			return
		}
		version, ok := row.SoleString()
		if !ok {
			e.versionErr = fmt.Errorf("version probe returned an unexpected shape")
			return
		}
		e.version = version
	})
	return e.version, e.versionErr
}

func (e *sqlExecutor) Close() error {
	return e.db.Close()
}
