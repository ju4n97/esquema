# Contributing to hclapi

It's recommended that you read the [documentation](https://ju4n97.github.io/hclapi/) for architecture and behavior. This project follows standard [Effective Go](https://go.dev/doc/effective_go) idioms to keep the codebase simple, fast, and easy to maintain.

## Development

Tasks are available in the [Taskfile](Taskfile.yaml).

## Testing

Prefer tests that exercise behavior through the public API:

```go
cfg, err := hclapi.Parse(manifest)
if err != nil {
    t.Fatal(err)
}

api, err := hclapi.New(cfg)
if err != nil {
    t.Fatal(err)
}
defer api.Close()

req := httptest.NewRequest(http.MethodGet, "/users", nil)
rec := httptest.NewRecorder()

api.ServeHTTP(rec, req)

if rec.Code != http.StatusOK {
    t.Fatalf("status = %d; want %d", rec.Code, http.StatusOK)
}
```

Use `httptest` for HTTP behavior and the [integration suite](test/integration/) for testing real external dependencies.

## Code

Prefer simple, concrete Go:

- Add an interface only when the consuming code needs one.
- Keep dependencies explicit. Avoid unnecessary layers, factories, repositories, service objects, event buses, or dependency-injection abstractions.
- Keep runtime configuration immutable after loading.
- Document code with GoDoc comments, including unexported functions, methods, and tests. Only omit comments for excessive or repetitive struct properties.

## Changes

Keep pull requests small and focused. Show the behavior in code or tests rather than explaining it at length.

Commit messages use Conventional Commits with the purpose of automated changelog generation:

```text
feat(engine): add HTTP step
fix(manifest): reject duplicate routes
test(validator): cover invalid query parameters
chore: update dependencies
```

Breaking changes use `!`:

```text
feat(engine)!: change step syntax
```

Include migration details in the commit body or footer.

## AI

AI tools are allowed for bounded engineering work such as code generation, test generation, refactoring, documentation, and other mechanical tasks.

Vibe coding is discouraged. Don't use AI to generate large or complex changes that you have not designed, reviewed, tested, and understood yourself. Architectural decisions and non-trivial behavior should be reasoned about by the contributor first.

Also, avoid committing editor, IDE, or AI-tool configuration such as `.vscode/`, `.zed/`, `.cursor/`, or `CLAUDE.md`. These files are typically personal or machine-specific and should not become part of the project's source of truth.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
