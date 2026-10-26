package trino

import (
	"context"
	"database/sql"
	"errors"
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
	// Version returns the cached server version. Only a successful probe is
	// cached: a failed probe is retried by the next call.
	Version(ctx context.Context) (string, error)
	// Close releases the pools.
	Close() error
}

var _ Executor = (*sqlExecutor)(nil)

// sqlExecutor keeps two pools over the same coordinator. Mutating statements
// go through execDB, whose connector has HTTP retries disabled, so a request
// that succeeded server-side but timed out client-side is never re-sent.
// Queries and the version probe go through db, which keeps the driver's
// transient-failure retries.
type sqlExecutor struct {
	db     *sql.DB
	execDB *sql.DB

	versionMu    sync.Mutex
	version      string
	versionKnown bool
	probe        *versionProbe
}

// versionProbe is one in-flight SELECT version(). Concurrent callers wait on
// done instead of starting their own probe; the result is shared.
type versionProbe struct {
	done    chan struct{}
	version string
	err     error
}

func (e *sqlExecutor) Exec(ctx context.Context, statement string, args ...any) error {
	_, err := e.execDB.ExecContext(ctx, statement, args...)
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
	e.versionMu.Lock()
	if e.versionKnown {
		version := e.version
		e.versionMu.Unlock()
		return version, nil
	}
	if probe := e.probe; probe != nil {
		e.versionMu.Unlock()
		select {
		case <-probe.done:
			return probe.version, probe.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	probe := &versionProbe{done: make(chan struct{})}
	e.probe = probe
	e.versionMu.Unlock()

	probe.version, probe.err = e.probeVersion(ctx)

	e.versionMu.Lock()
	if probe.err == nil {
		e.version = probe.version
		e.versionKnown = true
	}
	e.probe = nil
	close(probe.done)
	e.versionMu.Unlock()

	return probe.version, probe.err
}

func (e *sqlExecutor) probeVersion(ctx context.Context) (string, error) {
	row, ok, err := e.QueryOne(ctx, "SELECT version()")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("version probe returned no rows")
	}
	version, ok := row.SoleString()
	if !ok {
		return "", fmt.Errorf("version probe returned an unexpected shape")
	}
	return version, nil
}

func (e *sqlExecutor) Close() error {
	return errors.Join(e.db.Close(), e.execDB.Close())
}
