package manifest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/zclconf/go-cty/cty/function"
	"modernc.org/sqlite"

	"github.com/ju4n97/hclapi/internal/problem"
)

// sqlTokenRegex isolates single-quoted strings, multi-line/single-line comments,
// and @name placeholders. It ignores PostgreSQL operators like @> by requiring
// word characters immediately following the @ symbol.
var sqlTokenRegex = regexp.MustCompile(`(?s)('(\\.|''|[^'\\])*'|--[^\r\n]*|/\*.*?\*/|@([a-zA-Z0-9_]+))`)

// StepSQL executes parameterized relational database queries against configured pools.
type StepSQL struct {
	Name       string
	Connection string
	Query      string
	Args       Expr
	When       Expr
	Stream     bool
	Catches    []SQLCatch

	dialect       string
	compiledQuery string
	paramOrder    []string
}

// SQLCatch specifies interception criteria for database driver errors.
type SQLCatch struct {
	Code       string
	Constraint string
	Match      string
	Status     int
	Body       Expr
}

// StepName implements [Step].
func (s *StepSQL) StepName() string {
	return s.Name
}

// StepWhen implements [Step].
func (s *StepSQL) StepWhen() Expr {
	return s.When
}

// IsTerminal implements [Step] and returns false.
func (s *StepSQL) IsTerminal() bool {
	return false
}

// ValidateStep implements [StepValidator].
func (s *StepSQL) ValidateStep(m *Manifest) error {
	if s.Connection == "" {
		return fmt.Errorf("sql step %q missing connection identifier", s.Name)
	}

	conn, exists := m.Connections[s.Connection]
	if !exists {
		return fmt.Errorf("sql step %q references unknown connection %q", s.Name, s.Connection)
	}

	if conn.Type != "sql" {
		return fmt.Errorf("sql step %q cannot use non-sql connection %q (type: %s)", s.Name, s.Connection, conn.Type)
	}

	if strings.TrimSpace(s.Query) == "" {
		return fmt.Errorf("sql step %q query cannot be empty", s.Name)
	}

	for i, c := range s.Catches {
		if c.Code == "" && c.Constraint == "" && c.Match == "" {
			return fmt.Errorf("sql step %q catch block %d must define at least one of 'code', 'constraint', or 'match'", s.Name, i)
		}
		if c.Status != 0 && (c.Status < 100 || c.Status > 599) {
			return fmt.Errorf("sql step %q catch status %d outside valid HTTP range", s.Name, c.Status)
		}
	}

	s.dialect = conn.Engine
	if s.dialect == "mysql" {
		s.compiledQuery, s.paramOrder = compileMySQLPlaceholders(s.Query)
	}

	return nil
}

// ExecuteStep implements [StepExecutor].
func (s *StepSQL) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	db, err := ec.SQL(s.Connection)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", s.Name, err)
	}

	var rawArgs any
	if s.Args != nil {
		rawArgs, err = s.Args.Eval(ec.Scope())
		if err != nil {
			return nil, fmt.Errorf("step %q evaluate args: %w", s.Name, err)
		}
	}

	query, args, err := s.resolveQueryAndArgs(rawArgs)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", s.Name, err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		if catch := s.matchCatch(err); catch != nil {
			return nil, s.writeCatchResponse(ec, catch, err)
		}
		return nil, fmt.Errorf("step %q execute: %w", s.Name, err)
	}

	cols, err := rows.Columns()
	if err != nil {
		rows.Close()
		return nil, fmt.Errorf("step %q read columns: %w", s.Name, err)
	}

	if s.Stream {
		return s.streamRows(ctx, rows, cols), nil
	}
	defer rows.Close()

	if len(cols) == 0 {
		return map[string]any{
			"rows":          []map[string]any{},
			"row":           nil,
			"rows_affected": 0,
		}, nil
	}

	scannedRows, err := scanRows(rows, cols)
	if err != nil {
		return nil, fmt.Errorf("step %q scan rows: %w", s.Name, err)
	}

	var firstRow map[string]any
	if len(scannedRows) > 0 {
		firstRow = scannedRows[0]
	}

	return map[string]any{
		"rows":          scannedRows,
		"row":           firstRow,
		"rows_affected": len(scannedRows),
	}, nil
}

// streamRows unpacks active cursor rows into native map representations.
func (s *StepSQL) streamRows(ctx context.Context, rows *sql.Rows, cols []string) RecordSeq {
	return func(yield func(any, error) bool) {
		defer rows.Close()

		for rows.Next() {
			select {
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			default:
			}

			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}

			if err := rows.Scan(ptrs...); err != nil {
				yield(nil, err)
				return
			}

			row := make(map[string]any, len(cols))
			for i, col := range cols {
				val := values[i]
				if b, ok := val.([]byte); ok {
					val = string(b)
				}
				row[col] = val
			}

			if !yield(row, nil) {
				return
			}
		}

		if err := rows.Err(); err != nil {
			yield(nil, err)
		}
	}
}

// resolveQueryAndArgs inspects the evaluated argument format.
//
//   - If args is a slice ([]any), the query string is sent completely untouched,
//     allowing callers to write native dialect tokens ($1, ?, @p1) directly.
//   - If args is a map (map[string]any), it delegates named parameter binding
//     natively to pgx, go-mssqldb, or SQLite drivers, and uses pre-compiled ? tokens for MySQL.
func (s *StepSQL) resolveQueryAndArgs(rawArgs any) (string, []any, error) {
	if rawArgs == nil {
		return s.Query, nil, nil
	}

	if list, ok := rawArgs.([]any); ok {
		return s.Query, list, nil
	}

	argMap, ok := rawArgs.(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("expected args to evaluate to map or list, got %T", rawArgs)
	}

	switch s.dialect {
	case "postgres":
		return s.Query, []any{pgx.NamedArgs(argMap)}, nil

	case "sqlserver", "sqlite":
		namedArgs := make([]any, 0, len(argMap))
		for k, v := range argMap {
			namedArgs = append(namedArgs, sql.Named(k, v))
		}
		return s.Query, namedArgs, nil

	case "mysql":
		positional := make([]any, len(s.paramOrder))
		for i, paramName := range s.paramOrder {
			positional[i] = argMap[paramName]
		}
		return s.compiledQuery, positional, nil

	default:
		return s.Query, nil, fmt.Errorf("unsupported database dialect %q", s.dialect)
	}
}

// compileMySQLPlaceholders scans @name tokens in the query and replaces them with ?,
// recording the parameter names in sequence. Literals and comments are preserved untouched.
func compileMySQLPlaceholders(query string) (string, []string) {
	var paramOrder []string

	rewritten := sqlTokenRegex.ReplaceAllStringFunc(query, func(token string) string {
		if strings.HasPrefix(token, "'") || strings.HasPrefix(token, "--") || strings.HasPrefix(token, "/*") {
			return token
		}

		paramName := token[1:]
		paramOrder = append(paramOrder, paramName)
		return "?"
	})

	return rewritten, paramOrder
}

// matchCatch inspects driver-specific errors and returns the first matching catch definition.
func (s *StepSQL) matchCatch(err error) *SQLCatch {
	driverCode, constraintName := extractDriverErrorDetails(err)
	errString := err.Error()

	for i := range s.Catches {
		c := &s.Catches[i]

		if c.Code != "" && driverCode != "" {
			if c.Code == driverCode {
				return c
			}
			// Match SQLite primary code against extended codes (2067 & 0xFF == 19)
			if num, parseErr := strconv.Atoi(driverCode); parseErr == nil {
				if strconv.Itoa(num&0xFF) == c.Code {
					return c
				}
			}
		}

		if c.Constraint != "" && constraintName != "" && c.Constraint == constraintName {
			return c
		}

		if c.Match != "" && strings.Contains(errString, c.Match) {
			return c
		}
	}

	return nil
}

// writeCatchResponse streams an RFC 9457 Problem Details payload using [problem.Problem].
func (s *StepSQL) writeCatchResponse(exec StepExecutionContext, c *SQLCatch, originalErr error) error {
	status := c.Status
	if status == 0 {
		status = http.StatusBadRequest
	}

	p := problem.New(status, originalErr.Error())

	if c.Body != nil {
		bodyVal, err := c.Body.Eval(exec.Scope())
		if err == nil && bodyVal != nil {
			switch b := bodyVal.(type) {
			case problem.Problem:
				p = b
			case map[string]any:
				if st, ok := b["status"].(int64); ok && st != 0 {
					p.Status = int(st)
				}
				if t, ok := b["title"].(string); ok && t != "" {
					p.Title = t
				}
				if d, ok := b["detail"].(string); ok && d != "" {
					p.Detail = d
				}
			case string:
				p.Detail = b
			}
		}
	}

	problem.Write(exec.ResponseWriter(), p)
	return ErrPipelineHalted
}

// extractDriverErrorDetails extracts error codes and constraint names across database drivers.
func extractDriverErrorDetails(err error) (code, constraint string) {
	if err == nil {
		return "", ""
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code, pgErr.ConstraintName
	}

	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return strconv.FormatUint(uint64(myErr.Number), 10), ""
	}

	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		return strconv.Itoa(sqErr.Code()), ""
	}

	var msErr mssql.Error
	if errors.As(err, &msErr) {
		return strconv.FormatInt(int64(msErr.Number), 10), ""
	}

	return "", ""
}

// scanRows unpacks active cursor rows into native map representations.
func scanRows(rows *sql.Rows, cols []string) ([]map[string]any, error) {
	var results []map[string]any

	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any, len(cols))
		for i, col := range cols {
			val := values[i]
			switch v := val.(type) {
			case []byte:
				val = string(v)
			case time.Time:
				val = v.Format(time.RFC3339)
			}
			row[col] = val
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if results == nil {
		results = []map[string]any{}
	}
	return results, nil
}

// decodeStepSQL decodes the HCL block representation into a [*StepSQL].
func decodeStepSQL(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type catchBlock struct {
		Code       string         `hcl:"code,optional"`
		Constraint string         `hcl:"constraint,optional"`
		Match      string         `hcl:"match,optional"`
		Status     int            `hcl:"status,optional"`
		BodyExpr   hcl.Expression `hcl:"body,optional"`
	}

	type sqlBlock struct {
		Connection string         `hcl:"connection"`
		Query      string         `hcl:"query"`
		ArgsExpr   hcl.Expression `hcl:"args,optional"`
		WhenExpr   hcl.Expression `hcl:"when,optional"`
		Stream     bool           `hcl:"stream,optional"`
		Catches    []catchBlock   `hcl:"catch,block"`
	}

	var raw sqlBlock
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	step := &StepSQL{
		Name:       name,
		Connection: raw.Connection,
		Query:      raw.Query,
		Stream:     raw.Stream,
		Args:       NewExpr(raw.ArgsExpr, funcs),
		When:       NewExpr(raw.WhenExpr, funcs),
	}

	for _, c := range raw.Catches {
		step.Catches = append(step.Catches, SQLCatch{
			Code:       c.Code,
			Constraint: c.Constraint,
			Match:      c.Match,
			Status:     c.Status,
			Body:       NewExpr(c.BodyExpr, funcs),
		})
	}

	return step, nil
}
