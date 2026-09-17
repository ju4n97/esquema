package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
	"modernc.org/sqlite"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/problem"
)

var (
	leadingSingleLineComment = regexp.MustCompile(`^\s*--[^\n]*(\n|$)`)
	leadingMultiLineComment  = regexp.MustCompile(`(?s)^\s*/\*.*?\*/`)
	// sqlTokenRegex matches single-quoted literals, comments, and @parameters in a single pass.
	sqlTokenRegex = regexp.MustCompile(`(?s)('(\\.|''|[^'\\])*'|--[^\r\n]*|/\*.*?\*/|@([a-zA-Z0-9_]+))`)
)

// executeSQL executes parameterized relational database queries and records row outputs.
func (e *Engine) executeSQL(ctx *Context, w http.ResponseWriter, name string, step *config.SQLStep) error {
	conn, ok := e.sqls[step.Connection]
	if !ok {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("sql connection %q not found", step.Connection))
		problem.Write(w, p)
		return p
	}

	var evalArgs map[string]any
	if step.ArgsExpr != nil {
		rawArgs, err := ctx.EvalAny(step.ArgsExpr)
		if err == nil {
			evalArgs, _ = rawArgs.(map[string]any)
		}
	}

	query, args := rewriteNamedQuery(step.Query, conn.driver, evalArgs)

	if isRowProducingQuery(query) {
		rows, err := conn.db.QueryContext(ctx.RequestContext(), query, args...)
		if err != nil {
			return e.handleSQLError(ctx, w, step, conn.driver, err)
		}
		defer rows.Close()

		scanned, err := scanRows(rows)
		if err != nil {
			p := problem.New(http.StatusInternalServerError, fmt.Sprintf("scan error: %v", err))
			problem.Write(w, p)
			return p
		}

		var firstRow map[string]any
		if len(scanned) > 0 {
			firstRow = scanned[0]
		}

		ctx.SetStepResult(name, map[string]any{
			"rows":          scanned,
			"row":           firstRow,
			"rows_affected": len(scanned),
		})
		return nil
	}

	res, err := conn.db.ExecContext(ctx.RequestContext(), query, args...)
	if err != nil {
		return e.handleSQLError(ctx, w, step, conn.driver, err)
	}

	affected, _ := res.RowsAffected()
	ctx.SetStepResult(name, map[string]any{
		"rows":          []map[string]any{},
		"row":           nil,
		"rows_affected": affected,
	})
	return nil
}

// rewriteNamedQuery rewrites named parameters into driver-specific bind variables
// while safely skipping string literals, comments, and operator tokens.
func rewriteNamedQuery(query, driver string, args map[string]any) (string, []any) {
	var orderedArgs []any
	count := 1

	rewritten := sqlTokenRegex.ReplaceAllStringFunc(query, func(match string) string {
		// Ignore string literals and comments
		if strings.HasPrefix(match, "'") || strings.HasPrefix(match, "--") || strings.HasPrefix(match, "/*") {
			return match
		}

		paramName := match[1:]
		var val any
		if args != nil {
			val = args[paramName]
		}
		orderedArgs = append(orderedArgs, val)

		placeholder := formatPlaceholder(driver, count)
		count++
		return placeholder
	})

	return rewritten, orderedArgs
}

// formatPlaceholder returns the dialect-specific placeholder for the parameter sequence.
func formatPlaceholder(engine string, count int) string {
	switch config.SQLEngine(engine) {
	case config.SQLEnginePostgres:
		return "$" + strconv.Itoa(count)
	case config.SQLEngineSQLServer:
		return "@p" + strconv.Itoa(count)
	default:
		// SQLite and MySQL use ?
		return "?"
	}
}

// isRowProducingQuery checks whether a query produces rows by stripping comments and inspecting keywords.
func isRowProducingQuery(query string) bool {
	s := query
	for {
		if loc := leadingSingleLineComment.FindString(s); loc != "" {
			s = s[len(loc):]
			continue
		}
		if loc := leadingMultiLineComment.FindString(s); loc != "" {
			s = s[len(loc):]
			continue
		}
		break
	}
	trimmed := strings.ToUpper(strings.TrimSpace(s))

	for _, prefix := range []string{
		"SELECT", "WITH", "CALL", "EXEC", "EXECUTE", "PRAGMA", "SHOW", "DESC", "EXPLAIN", "VALUES", "TABLE",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	return strings.Contains(trimmed, "RETURNING") || strings.Contains(trimmed, "OUTPUT")
}

// scanRows scans all records and result sets from an active database cursor.
func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	var results []map[string]any

	for {
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}

		if len(cols) > 0 {
			for rows.Next() {
				values := make([]any, len(cols))
				valuePtrs := make([]any, len(cols))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				if err := rows.Scan(valuePtrs...); err != nil {
					return nil, fmt.Errorf("scan column: %w", err)
				}

				rowMap := make(map[string]any, len(cols))
				for i, colName := range cols {
					val := values[i]
					switch v := val.(type) {
					case []byte:
						val = string(v)
					case time.Time:
						val = v.Format(time.RFC3339)
					}
					rowMap[colName] = val
				}
				results = append(results, rowMap)
			}
			if err := rows.Err(); err != nil {
				return nil, fmt.Errorf("read rows: %w", err)
			}
		}

		if !rows.NextResultSet() {
			break
		}
	}

	if results == nil {
		results = []map[string]any{}
	}
	return results, nil
}

// handleSQLError checks error codes against configured catch blocks and writes matching responses.
func (e *Engine) handleSQLError(ctx *Context, w http.ResponseWriter, step *config.SQLStep, driver string, err error) error {
	code := extractSQLErrorCode(err, driver)

	for _, c := range step.Catches {
		matched := false
		if code != "" {
			matched = matchSQLErrorCode(driver, code, c.Code)
		} else {
			matched = strings.Contains(err.Error(), c.Code)
		}

		if matched {
			status := c.Status
			if status == 0 {
				status = http.StatusBadRequest
			}

			body, evalErr := ctx.EvalAny(c.BodyExpr)
			if evalErr != nil || body == nil {
				body = problem.New(status, err.Error())
			}

			w.Header().Set("Content-Type", problem.ContentType)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
			return err
		}
	}

	p := problem.New(http.StatusInternalServerError, err.Error())
	problem.Write(w, p)
	return err
}

// extractSQLErrorCode inspects errors from various SQL drivers to obtain database-specific error codes.
func extractSQLErrorCode(err error, engine string) string {
	if err == nil {
		return ""
	}

	switch config.SQLEngine(engine) {
	case config.SQLEnginePostgres:
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			return pgErr.Code
		}
	case config.SQLEngineMySQL:
		if myErr, ok := errors.AsType[*mysql.MySQLError](err); ok {
			return strconv.FormatUint(uint64(myErr.Number), 10)
		}
	case config.SQLEngineSQLServer:
		if msErr, ok := errors.AsType[mssql.Error](err); ok {
			return strconv.FormatInt(int64(msErr.Number), 10)
		}
	case config.SQLEngineSQLite:
		if sqErr, ok := errors.AsType[*sqlite.Error](err); ok {
			return strconv.Itoa(sqErr.Code())
		}
	}

	type sqlStateCoder interface{ SQLState() string }
	if coder, ok := err.(sqlStateCoder); ok {
		return coder.SQLState()
	}

	return ""
}

// matchSQLErrorCode compares extracted driver error codes against target configured codes.
func matchSQLErrorCode(driver, actualCode, targetCode string) bool {
	if actualCode == "" || targetCode == "" {
		return false
	}
	if actualCode == targetCode {
		return true
	}
	if strings.HasPrefix(strings.ToLower(driver), "sqlite") {
		if num, err := strconv.Atoi(actualCode); err == nil {
			if strconv.Itoa(num&0xFF) == targetCode {
				return true
			}
		}
	}
	if strings.Contains(strings.ToLower(driver), "postgres") {
		if strings.HasPrefix(actualCode, targetCode) {
			return true
		}
	}
	return false
}
