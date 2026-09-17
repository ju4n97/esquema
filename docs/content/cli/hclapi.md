---
title: hclapi
---

<!-- Generated automatically by hclapi docs. Do not edit directly. -->

# NAME

hclapi - Type-safe, declarative API runtime powered by HCL.

# SYNOPSIS

hclapi

**Usage**:

```
hclapi [GLOBAL OPTIONS] [command [COMMAND OPTIONS]] [ARGUMENTS...]
```

# COMMANDS

## serve, s

Start the HTTP API server from compiled manifests

**--config, -c, --manifests, -m**="": Manifest file, directory, or glob pattern (can be specified multiple times)

**--host, -H**="": Host address to bind the listener (overrides manifest)

**--log-format**="": Log output format (text, json) (default: "text")

**--log-level**="": Log level (debug, info, warn, error) (default: "info")

**--port, -p**="": Port to bind the listener (overrides manifest) (default: 0)

**--verbose, -v**: Enable debug logging (shorthand for --log-level=debug)

## openapi, oas, spec

Export OpenAPI 3.1 specification for the compiled manifests

**--config, -c**="": Manifest file, directory, or glob pattern

**--format, -f**="": Output format (json, yaml) (default: "json")

**--output, -o**="": Write output to a file instead of stdout

## lint, check, validate

Validate manifest schemas, types, and referential integrity

**--config, -c**="": Manifest file, directory, or glob pattern

## routes

List all compiled endpoints and pipeline step sequences

**--config, -c**="": Manifest file, directory, or glob pattern

## version, v

Print version and build metadata

**--short, -s**: Print only the semantic version string
