server {
  host = "127.0.0.1"
  port = 8080
}

route "GET /docs" {
  docs {
    renderer = "elements"
  }
}

route "GET /openapi.json" {
  spec {
    format = "json"
  }
}

route "GET /api/v1/sky/mars-age/{earth_years}" {
  summary = "Converts an age in Earth years to Mars years via Go callback"
  tag     = "astronomy"

  request {
    path "earth_years" {
      type        = number
      required    = true
      description = "Age in Earth years"
    }
  }

  go "convert" {
    use = "astronomy.mars_age"
    args = {
      earth_years = ctx.request.path.earth_years
    }
  }

  respond {
    status = 200
    body   = steps.convert.result
  }
}