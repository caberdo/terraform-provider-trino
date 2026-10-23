package trinotest

import (
	"context"
	"fmt"
	"sync"

	"github.com/caberdo/terraform-provider-trino/internal/trino"
)

// Call is one statement the code under test sent through the fake.
type Call struct {
	Op        string
	Statement string
	Args      []any
}

// Step is one scripted response. Exec uses Err; Query uses Rows and Err.
type Step struct {
	Rows []trino.Row
	Err  error
}

// FakeExecutor implements trino.Executor from a queue of steps, recording every
// call so resource tests can assert the exact SQL and bound arguments.
type FakeExecutor struct {
	Steps         []Step
	Calls         []Call
	ServerVersion string
	VersionErr    error

	mu   sync.Mutex
	next int
}

var _ trino.Executor = (*FakeExecutor)(nil)

// Exec records the call and returns the next step's error.
func (f *FakeExecutor) Exec(_ context.Context, statement string, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	step, err := f.take("exec", statement, args)
	if err != nil {
		return err
	}
	return step.Err
}

// Query records the call and returns the next step's rows.
func (f *FakeExecutor) Query(_ context.Context, statement string, args ...any) ([]trino.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	step, err := f.take("query", statement, args)
	if err != nil {
		return nil, err
	}
	return step.Rows, step.Err
}

// QueryOne delegates to Query so one queued step serves either call.
func (f *FakeExecutor) QueryOne(ctx context.Context, statement string, args ...any) (trino.Row, bool, error) {
	rows, err := f.Query(ctx, statement, args...)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows[0], true, nil
}

// Version returns the scripted version without consuming a step.
func (f *FakeExecutor) Version(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ServerVersion, f.VersionErr
}

// Close is a no-op.
func (f *FakeExecutor) Close() error {
	return nil
}

func (f *FakeExecutor) take(op, statement string, args []any) (Step, error) {
	f.Calls = append(f.Calls, Call{Op: op, Statement: statement, Args: args})
	if f.next >= len(f.Steps) {
		return Step{}, fmt.Errorf("trinotest: unexpected %s: %s", op, statement)
	}
	step := f.Steps[f.next]
	f.next++
	return step, nil
}
