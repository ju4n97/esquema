# hclapi

[![Go Reference](https://img.shields.io/badge/Go_Reference-pkg.go.dev-007D9C?style=flat-square)](https://pkg.go.dev/github.com/ju4n97/hclapi)
[![Release](https://img.shields.io/github/v/release/ju4n97/hclapi?style=flat-square\&label=Release)](https://github.com/ju4n97/hclapi/releases/latest)
[![CI](https://img.shields.io/github/actions/workflow/status/ju4n97/hclapi/ci.yaml?style=flat-square\&label=CI)](https://github.com/ju4n97/hclapi/actions/workflows/ci.yaml)

hclapi is a declarative API runtime powered by HCL.

Define HTTP routes, validation, SQL, Valkey, Starlark, Go callbacks, and OpenAPI documentation without generating application code.

Manifests are loaded, validated, and compiled at startup, then executed directly at request time.

[Documentation](https://ju4n97.github.io/hclapi/) · [Examples](./examples)

## Example

```hcl
server {
  host = "0.0.0.0"
  port = 8080
}

connection "sql" "main" {
  engine = "postgres"
  source = env("DATABASE_URL")
}

route "POST /users" {
  request {
    body {
      field "email" {
        type     = "string"
        format   = "email"
        required = true
      }

      field "name" {
        type     = "string"
        required = true
      }
    }
  }

  step "sql" "create" {
    connection = "main"

    query = <<-SQL
      INSERT INTO users (email, name)
      VALUES (@email, @name)
      RETURNING id, email, name
    SQL

    args = {
      email = ctx.request.body.email
      name  = ctx.request.body.name
    }
  }

  respond {
    status = 201
    body   = steps.create.row
  }
}
```

## Go

hclapi is also embeddable:

```go
config, err := hclapi.Load("routes/*.hcl")
if err != nil {
    log.Fatal(err)
}

engine, err := hclapi.New(config)
if err != nil {
    log.Fatal(err)
}
defer engine.Close()

http.ListenAndServe(":8080", engine)
```

Custom Go behavior can be registered with `hclapi.WithStep`. More information available in the [Go integration guide](https://ju4n97.github.io/hclapi/guides/go).

## Install

```bash
go install github.com/ju4n97/hclapi/cmd/hclapi@latest
```

Or use the release binaries and container images documented in the [installation guide](https://ju4n97.github.io/hclapi/installation).

## Documentation

See [hclapi documentation](https://ju4n97.github.io/hclapi/).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
