# Camunda External Task Client

A Go client for Camunda 7 external tasks with fluent API support and built-in worker infrastructure.

## Features

### Core Client
- Fluent API for all external task operations

# Camunda External Task Client

This module provides a Go client for Camunda 7 external tasks and a small worker framework with a fluent API.

The README below is concise and practical: quick start, recommended tuning for high load, and an example Docker run for a tuned engine.

## Key features

- Fluent builders for External Task operations (fetchAndLock, complete, failure, extend lock, unlock)
- Type-safe process variables
- Worker with topic-based handler registration
- Concurrency control, graceful shutdown, and panic recovery
- Middleware support (logging, tracing)

## Install

```bash
go get github.com/nativebpm/camunda
```

## Quick start

See the runnable examples in the `examples/` directory for full working samples (for example, `examples/loan-granting`).

## Examples

See the `examples/` directory for working examples (for example, `examples/loan-granting`).

## License

See the repository LICENSE file.
