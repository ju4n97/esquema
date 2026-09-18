# hclapi

[![Go Reference](https://img.shields.io/badge/Go_Reference-pkg.go.dev-007D9C?style=flat-square)](https://pkg.go.dev/github.com/ju4n97/hclapi)
[![Release](https://img.shields.io/github/v/release/ju4n97/hclapi?style=flat-square&label=Release)](https://github.com/ju4n97/hclapi/releases/latest)
[![CI](https://img.shields.io/github/actions/workflow/status/ju4n97/hclapi/ci.yaml?style=flat-square&label=CI)](https://github.com/ju4n97/hclapi/actions/workflows/ci.yaml)

hclapi is a lightweight, declarative API runtime powered by HashiCorp HCL.

Define HTTP endpoints, input validation schemas, database queries, caching, sandboxed data transformations, real-time streams, and OpenAPI 3.1 specifications in human-readable manifests without writing boilerplate backend routing code.

Manifests are verified and precompiled at startup, then executed sequentially at request time.

[Documentation](https://ju4n97.github.io/hclapi/) · [Quickstart](https://ju4n97.github.io/hclapi/docs/quickstart) · [Examples](./examples)

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
        type     = string
        format   = "email"
        required = true
      }

      field "name" {
        type     = string
        required = true
      }
    }
  }

  sql "create" {
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

    catch {
      code   = "23505"
      status = 409
      body   = problem(409, "Email is already registered")
    }
  }

  respond {
    status = 201
    body   = steps.create.row
  }
}
```

## Go integration

hclapi is also distributed as an embeddable Go library:

```go
package main

import (
 "log"
 "net/http"

 "github.com/ju4n97/hclapi"
)

func main() {
  manifest, err := hclapi.Load("routes/*.hcl")
  if err != nil {
    log.Fatal(err)
  }

  app, err := hclapi.New(manifest)
  if err != nil {
    log.Fatal(err)
  }
  defer app.Close()

  http.ListenAndServe(":8080", app)
}
```

Custom Go behavior can be registered with `hclapi.WithStep`. See the [Go integration guide](https://ju4n97.github.io/hclapi/guides/go) for details.

## Install

Install the standalone CLI daemon via `go install`:

```bash
go install github.com/ju4n97/hclapi/cmd/hclapi@latest
```

Or use precompiled binary archives, Linux packages (`.deb`, `.rpm`, `.apk`, `.pkg.tar.zst`), and container images documented in the [installation guide](https://ju4n97.github.io/hclapi/docs/installation).

## Documentation

See the full [hclapi documentation](https://ju4n97.github.io/hclapi/).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
