server {
  host = "127.0.0.1"
  port = 8080
}

openapi {
  title       = "Todo Persistence API"
  version     = "1.0.0"
  description = "Full relational CRUD workflow backed by embedded SQLite"
}

connection "sql" "main" {
  engine = "sqlite"
  source = "file:todos.db?mode=rwc"
  pool {
    max_open = 1
  }
}

schema "Todo" {
  field "id" {
    type     = integer
    required = true
  }
  field "title" {
    type       = string
    required   = true
    min_length = 1
  }
  field "completed" {
    type    = boolean
    default = false
  }
  field "created_at" {
    type   = string
    format = "date-time"
  }
}

schema "TodoCreate" {
  field "title" {
    type       = string
    required   = true
    min_length = 1
  }
}

schema "TodoUpdate" {
  field "title" {
    type       = string
    min_length = 1
  }
  field "completed" {
    type = boolean
  }
}

route "GET /docs" {
  docs {
    renderer = "scalar"
  }
}

route "GET /openapi.json" {
  spec {
    format = "json"
  }
}

route "GET /api/v1/todos" {
  summary = "List all stored todos"
  tag     = "todos"

  sql "list" {
    connection = "main"
    query      = "SELECT id, title, completed, created_at FROM todos ORDER BY id DESC"
  }

  respond {
    status = 200
    schema = list(Todo)
    body   = steps.list.rows
  }
}

route "POST /api/v1/todos" {
  summary = "Create a new todo item"
  tag     = "todos"

  request {
    body = TodoCreate
  }

  starlark "sanitize" {
    source = <<-STARLARK
      def execute(ctx):
          body = ctx["request"]["body"] or {}
          return {
              "title": body.get("title", "").strip()
          }
    STARLARK
  }

  sql "insert" {
    connection = "main"
    query      = <<-SQL
      INSERT INTO todos (title)
      VALUES (@title)
      RETURNING id, title, completed, created_at
    SQL
    args = {
      title = steps.sanitize.result.title
    }
    catch {
      code   = "19"
      status = 409
      body   = problem(409, "A todo item with this title already exists")
    }
  }

  respond {
    status = 201
    schema = Todo
    body   = steps.insert.row
  }
}

route "GET /api/v1/todos/{id}" {
  summary = "Fetch a single todo by ID"
  tag     = "todos"

  request {
    path "id" {
      type     = integer
      required = true
    }
  }

  sql "fetch" {
    connection = "main"
    query      = "SELECT id, title, completed, created_at FROM todos WHERE id = @id"
    args = {
      id = ctx.request.path.id
    }
  }

  respond {
    when   = steps.fetch.rows_affected == 0
    status = 404
    body   = problem(404, "Todo item not found")
  }

  respond {
    status = 200
    schema = Todo
    body   = steps.fetch.row
  }
}

route "PUT /api/v1/todos/{id}" {
  summary = "Update an existing todo item"
  tag     = "todos"

  request {
    path "id" {
      type     = integer
      required = true
    }
    body = TodoUpdate
  }

  sql "update" {
    connection = "main"
    query      = <<-SQL
      UPDATE todos
      SET
        title = COALESCE(@title, title),
        completed = COALESCE(@completed, completed)
      WHERE id = @id
      RETURNING id, title, completed, created_at
    SQL
    args = {
      id        = ctx.request.path.id
      title     = ctx.request.body.title
      completed = ctx.request.body.completed
    }
  }

  respond {
    when   = steps.update.rows_affected == 0
    status = 404
    body   = problem(404, "Todo item not found")
  }

  respond {
    status = 200
    schema = Todo
    body   = steps.update.row
  }
}

route "DELETE /api/v1/todos/{id}" {
  summary = "Delete a todo item"
  tag     = "todos"

  request {
    path "id" {
      type     = integer
      required = true
    }
  }

  sql "delete" {
    connection = "main"
    query      = "DELETE FROM todos WHERE id = @id"
    args = {
      id = ctx.request.path.id
    }
  }

  respond {
    when   = steps.delete.rows_affected == 0
    status = 404
    body   = problem(404, "Todo item not found")
  }

  respond {
    status = 204
  }
}