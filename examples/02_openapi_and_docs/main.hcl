server {
  host = "127.0.0.1"
  port = 8080
}

openapi {
  title       = "Acme Documentation Showcase"
  version     = "1.0.0"
  description = "Demonstration of multiple interactive documentation portals and raw OpenAPI 3.1 artifacts."

  server {
    url         = "/"
    description = "Current server origin"
  }

  tag {
    name        = "system"
    description = "Core runtime and health probes"
  }

  contact {
    name  = "API Architecture Team"
    email = "architecture@example.com"
    url   = "https://example.com/support"
  }

  license {
    name = "MIT"
    url  = "https://opensource.org/licenses/MIT"
  }
}

route "GET /openapi.json" {
  spec {
    format = "json"
  }
}

route "GET /openapi.yaml" {
  spec {
    format = "yaml"
  }
}

route "GET /docs" {
  docs {
    renderer = "scalar"
  }
}

route "GET /docs/swagger" {
  docs {
    renderer = "swagger"
  }
}

route "GET /docs/elements" {
  docs {
    renderer = "elements"
  }
}

route "GET /docs/redoc" {
  docs {
    renderer = "redoc"
  }
}

route "GET /api/v1/ping" {
  summary = "Simple latency check"
  tag     = "system"

  respond {
    status = 200
    body = {
      status = "pong"
    }
  }
}