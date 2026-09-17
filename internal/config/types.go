// Package config defines the verified runtime models and enum types for the hclapi engine.
package config

import (
	"errors"
	"fmt"
	"strings"
)

// StepType defines the exact execution category of a pipeline step.
type StepType string

const (
	// StepTypeSQL executes parameterized relational database statements.
	StepTypeSQL StepType = "sql"

	// StepTypeValkey executes caching operations against Valkey instances.
	StepTypeValkey StepType = "valkey"

	// StepTypeHTTP performs outbound HTTP requests to external services.
	StepTypeHTTP StepType = "http"

	// StepTypeStarlark executes sandboxed Starlark scripts.
	StepTypeStarlark StepType = "starlark"

	// StepTypeGo invokes a registered native Go callback function.
	StepTypeGo StepType = "go"

	// StepTypeRespond serializes an HTTP response and terminates pipeline execution.
	StepTypeRespond StepType = "respond"

	// StepTypeDocs renders an interactive API documentation viewer.
	StepTypeDocs StepType = "docs"

	// StepTypeSpec serves compiled OpenAPI specification documents.
	StepTypeSpec StepType = "spec"
)

// ParseStepType validates that a string strictly matches an allowed StepType.
func ParseStepType(raw string) (StepType, error) {
	switch StepType(raw) {
	case StepTypeSQL, StepTypeValkey, StepTypeHTTP, StepTypeStarlark,
		StepTypeGo, StepTypeRespond, StepTypeDocs, StepTypeSpec:
		return StepType(raw), nil
	default:
		return "", fmt.Errorf("invalid step type %q (allowed: sql, valkey, http, starlark, go, respond, docs, spec)", raw)
	}
}

// ConnectionType defines the category of an external connection pool.
type ConnectionType string

const (
	// ConnectionTypeSQL identifies relational database connection pools.
	ConnectionTypeSQL ConnectionType = "sql"

	// ConnectionTypeValkey identifies Valkey key-value connection pools.
	ConnectionTypeValkey ConnectionType = "valkey"
)

// ParseConnectionType validates that a string strictly matches an allowed ConnectionType.
func ParseConnectionType(raw string) (ConnectionType, error) {
	switch ConnectionType(raw) {
	case ConnectionTypeSQL, ConnectionTypeValkey:
		return ConnectionType(raw), nil
	default:
		return "", fmt.Errorf("invalid connection type %q (allowed: sql, valkey)", raw)
	}
}

// SQLEngine specifies the canonical relational database engine.
type SQLEngine string

const (
	// SQLEnginePostgres identifies PostgreSQL databases.
	SQLEnginePostgres SQLEngine = "postgres"

	// SQLEngineMySQL identifies MySQL and MariaDB databases.
	SQLEngineMySQL SQLEngine = "mysql"

	// SQLEngineSQLite identifies SQLite embedded databases.
	SQLEngineSQLite SQLEngine = "sqlite"

	// SQLEngineSQLServer identifies Microsoft SQL Server databases.
	SQLEngineSQLServer SQLEngine = "sqlserver"
)

// ParseSQLEngine validates that a string strictly matches an allowed canonical SQLEngine.
func ParseSQLEngine(raw string) (SQLEngine, error) {
	switch SQLEngine(strings.ToLower(strings.TrimSpace(raw))) {
	case SQLEnginePostgres:
		return SQLEnginePostgres, nil
	case SQLEngineMySQL:
		return SQLEngineMySQL, nil
	case SQLEngineSQLite:
		return SQLEngineSQLite, nil
	case SQLEngineSQLServer:
		return SQLEngineSQLServer, nil
	case "postgresql", "pgx":
		return "", fmt.Errorf("invalid engine %q: use canonical name %q", raw, SQLEnginePostgres)
	case "sqlite3":
		return "", fmt.Errorf("invalid engine %q: use canonical name %q", raw, SQLEngineSQLite)
	case "mariadb":
		return "", fmt.Errorf("invalid engine %q: use canonical name %q", raw, SQLEngineMySQL)
	case "mssql":
		return "", fmt.Errorf("invalid engine %q: use canonical name %q", raw, SQLEngineSQLServer)
	default:
		return "", fmt.Errorf("unsupported sql engine %q (allowed: postgres, mysql, sqlite, sqlserver)", raw)
	}
}

// ValkeyOp defines the caching operation to perform.
type ValkeyOp string

const (
	// ValkeyOpGet retrieves a string value by key.
	ValkeyOpGet ValkeyOp = "get"

	// ValkeyOpSet sets a string value with an optional expiration.
	ValkeyOpSet ValkeyOp = "set"

	// ValkeyOpDel removes a key from the store.
	ValkeyOpDel ValkeyOp = "del"
)

// ParseValkeyOp validates that an operation string strictly matches an allowed ValkeyOp.
func ParseValkeyOp(raw string) (ValkeyOp, error) {
	switch ValkeyOp(raw) {
	case ValkeyOpGet, ValkeyOpSet, ValkeyOpDel:
		return ValkeyOp(raw), nil
	default:
		return "", fmt.Errorf("invalid valkey op %q (allowed: get, set, del)", raw)
	}
}

// DocsRenderer specifies the UI viewer technology to render.
type DocsRenderer string

const (
	// DocsRendererScalar renders the Scalar interactive API reference portal.
	DocsRendererScalar DocsRenderer = "scalar"

	// DocsRendererSwagger renders the Swagger UI portal.
	DocsRendererSwagger DocsRenderer = "swagger"

	// DocsRendererElements renders the Stoplight Elements portal.
	DocsRendererElements DocsRenderer = "elements"

	// DocsRendererRedoc renders the Redoc documentation portal.
	DocsRendererRedoc DocsRenderer = "redoc"
)

// ParseDocsRenderer validates that a renderer strictly matches an allowed DocsRenderer.
func ParseDocsRenderer(raw string) (DocsRenderer, error) {
	if raw == "" {
		return DocsRendererScalar, nil
	}
	switch DocsRenderer(raw) {
	case DocsRendererScalar, DocsRendererSwagger, DocsRendererElements, DocsRendererRedoc:
		return DocsRenderer(raw), nil
	default:
		return "", fmt.Errorf("invalid docs renderer %q (allowed: scalar, swagger, elements, redoc)", raw)
	}
}

// SpecFormat specifies the serialization encoding for OpenAPI specifications.
type SpecFormat string

const (
	// SpecFormatJSON serializes specifications as indented JSON.
	SpecFormatJSON SpecFormat = "json"

	// SpecFormatYAML serializes specifications as YAML.
	SpecFormatYAML SpecFormat = "yaml"
)

// ParseSpecFormat validates that a format strictly matches an allowed SpecFormat.
func ParseSpecFormat(raw string) (SpecFormat, error) {
	if raw == "" {
		return SpecFormatJSON, nil
	}
	switch SpecFormat(raw) {
	case SpecFormatJSON, SpecFormatYAML:
		return SpecFormat(raw), nil
	default:
		return "", fmt.Errorf("invalid spec format %q (allowed: json, yaml)", raw)
	}
}

// DataType specifies primitive data types strictly adhering to OpenAPI 3.1.
type DataType string

const (
	// DataTypeString represents textual data.
	DataTypeString DataType = "string"

	// DataTypeInteger represents whole numbers.
	DataTypeInteger DataType = "integer"

	// DataTypeNumber represents any numeric value, including floating-point numbers.
	DataTypeNumber DataType = "number"

	// DataTypeBoolean represents true or false values.
	DataTypeBoolean DataType = "boolean"

	// DataTypeArray represents an ordered sequence of elements.
	DataTypeArray DataType = "array"

	// DataTypeObject represents an unordered mapping of key-value pairs.
	DataTypeObject DataType = "object"
)

// ParseDataType validates that a type strictly adheres to OpenAPI 3.1 primitives.
func ParseDataType(raw string) (DataType, error) {
	switch DataType(raw) {
	case DataTypeString, DataTypeInteger, DataTypeNumber, DataTypeBoolean, DataTypeArray, DataTypeObject:
		return DataType(raw), nil
	default:
		return "", fmt.Errorf("invalid type %q (allowed: string, integer, number, boolean, array, object)", raw)
	}
}

// ParseFieldType parses a field type string into its base DataType, optional SchemaRef, and optional ItemsType.
func ParseFieldType(raw string) (DataType, string, DataType, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", errors.New("field type cannot be empty")
	}

	// Array notation: []T
	if after, ok := strings.CutPrefix(raw, "[]"); ok {
		elem := strings.TrimSpace(after)
		if elem == "" {
			return "", "", "", errors.New("array element type cannot be empty")
		}

		// Array of primitives: []string, []integer, etc.
		if dt, err := ParseDataType(elem); err == nil {
			return DataTypeArray, "", dt, nil
		}

		// Array of custom schemas: []User
		return DataTypeArray, elem, "", nil
	}

	// Flat primitive: string, integer, number, boolean, array, object
	if dt, err := ParseDataType(raw); err == nil {
		return dt, "", "", nil
	}

	// Direct custom schema reference: User
	return DataTypeObject, raw, "", nil
}

// Format defines standard semantic string formats recognized in OpenAPI 3.1.
type Format string

const (
	// FormatEmail defines RFC 5322 standard email addresses.
	FormatEmail Format = "email"

	// FormatUUID defines RFC 4122 universally unique identifiers.
	FormatUUID Format = "uuid"

	// FormatDateTime defines RFC 3339 date-time timestamps.
	FormatDateTime Format = "date-time"

	// FormatDate defines full-date representations formatted as YYYY-MM-DD.
	FormatDate Format = "date"

	// FormatURI defines full RFC 3986 universal resource identifiers.
	FormatURI Format = "uri"

	// FormatIPv4 defines standard dot-decimal IPv4 addresses.
	FormatIPv4 Format = "ipv4"

	// FormatIPv6 defines standard colon-separated IPv6 addresses.
	FormatIPv6 Format = "ipv6"

	// FormatHostname defines RFC 1123 compliant domain names.
	FormatHostname Format = "hostname"
)

// ValidateFormat checks whether a format string is a recognized OpenAPI standard.
func ValidateFormat(raw string) error {
	if raw == "" {
		return nil
	}
	switch Format(strings.ToLower(raw)) {
	case FormatEmail, FormatUUID, FormatDateTime, FormatDate, FormatURI, FormatIPv4, FormatIPv6, FormatHostname:
		return nil
	default:
		return fmt.Errorf("unrecognized format %q (supported: email, uuid, date-time, date, uri, ipv4, ipv6, hostname)", raw)
	}
}
