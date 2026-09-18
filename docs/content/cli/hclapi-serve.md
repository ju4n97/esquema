---
title: hclapi serve
---

<!-- Generated automatically by hclapi docs. Do not edit directly. -->

# NAME

serve - Start the HTTP API server from compiled manifests

# SYNOPSIS

serve

```
[--config|-c|--manifests|-m]=[value]
[--host|-H]=[value]
[--log-format]=[value]
[--log-level]=[value]
[--port|-p]=[value]
[--verbose|-v]
```

**Usage**:

```
serve [GLOBAL OPTIONS] [command [COMMAND OPTIONS]] [ARGUMENTS...]
```

# GLOBAL OPTIONS

**--config, -c, --manifests, -m**="": Manifest file, directory, or glob pattern (repeatable)

**--host, -H**="": Host address to bind the listener (overrides manifest)

**--log-format**="": Log output format (text, json) (default: "text")

**--log-level**="": Log level (debug, info, warn, error) (default: "info")

**--port, -p**="": Port to bind the listener (overrides manifest) (default: 0)

**--verbose, -v**: Enable debug logging (shorthand for --log-level=debug)
