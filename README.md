# Camunda External Task Client

A Go client for Camunda 7 external tasks with fluent API support and built-in worker infrastructure.

## Features

### Core Client
- Fluent API for all external task operations
- Fetch and lock external tasks
- Complete tasks with variables
- Handle task failures with retry logic
- Extend and unlock task locks
- Middleware support for logging and tracing
- Structured logging with slog
- Type-safe variable handling
- Process deployment support
- Process instance management

### Worker Infrastructure
- **Handler-based architecture** - Clean separation of concerns
- **Topic routing** - Automatically routes tasks to registered handlers
- **Error handling** - Automatic failure reporting to Camunda
- **Concurrent processing** - Each task processed in a separate goroutine
- **Graceful shutdown** - Context-aware cancellation
- **Configurable** - Adjust max tasks, poll interval, etc.

## Installation

```bash
go get github.com/nativebpm/camunda
```

## Quick Start

### Basic Worker with Handlers

```go
package main

import (
    "context"
    "log/slog"
    "github.com/nativebpm/camunda"
)

// Define your handler
type MyHandler struct {
    logger *slog.Logger
}

func (h *MyHandler) Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask) error {
    h.logger.Info("Processing task", "taskID", task.ID)
    
    // Your business logic here
    
    // Complete the task
    return client.Complete(task.ID).
        Context(ctx).
        Variable("result", camunda.StringVariable("success")).
        Execute()
}

func main() {
    logger := slog.Default()
    
    // Create client
    client, _ := camunda.NewClient("http://localhost:8080", "my-worker")
    client.WithLogger(logger)
    
    // Create worker
    worker := camunda.NewWorker(client, logger)
    
    // Register handlers
    handler := &MyHandler{logger: logger}
    worker.RegisterHandler("myTopic", handler, 60000, []string{"var1"})
    
    // Configure and start
    worker.SetMaxTasks(10).SetPollInterval(5 * time.Second)
    worker.Start(context.Background())
}
```

## Examples

Check out the [examples](./examples) directory for complete working examples:

- **[loan-granting](./examples/loan-granting)** - Complete external task worker with BPMN deployment, process start, and handler-based architecture

## API Reference

### Worker API

#### Creating a Worker

```go
worker := camunda.NewWorker(client, logger)
```

#### Registering Handlers

```go
worker.RegisterHandler(
    "topicName",    // Topic to subscribe to
    handler,        // TaskHandler implementation
    60000,          // Lock duration in ms
    []string{"var"} // Variables to fetch
)
```

#### Configuring Worker

```go
worker.SetMaxTasks(10)                     // Max tasks per poll
worker.SetPollInterval(5 * time.Second)    // Poll interval when no tasks
```

#### Starting Worker

```go
worker.Start(ctx)  // Blocking call, runs until context is cancelled
```

### TaskHandler Interface

All handlers must implement:

```go
type TaskHandler interface {
    Handle(ctx context.Context, client *Client, task ExternalTask) error
}
```

If `Handle` returns an error, the worker automatically reports a failure to Camunda with retry configuration.

### Client Creation

- `NewClient(hostURL, workerID)` - Create a new client (automatically adds `/engine-rest`)
- `WithLogger(logger)` - Add logging middleware
- `Use(middleware)` - Add custom middleware

### Client API

#### Task Operations

- `FetchAndLock(ctx, topics, maxTasks, asyncTimeout)` - Fetch and lock tasks
- `Complete(taskID)` - Create a completion builder
- `Failure(taskID)` - Create a failure builder
- `ExtendLock(taskID, newDuration)` - Create a lock extension builder
- `Unlock(taskID)` - Create an unlock builder
- ~~`PollTasks(ctx, topics, maxTasks, handler)`~~ - **Deprecated: Use Worker.Start() instead**

#### Process Operations

- `DeployProcess(ctx, deploymentName, reader, filename)` - Deploy BPMN process
- `StartProcessInstance(ctx, processDefinitionKey, variables)` - Start process instance

### Variable Types

Type-safe variable constructors:

```go
camunda.StringVariable("hello")
camunda.IntVariable(42)
camunda.LongVariable(9223372036854775807)
camunda.DoubleVariable(3.14)
camunda.BooleanVariable(true)
camunda.DateVariable(time.Now())
camunda.JSONVariable(map[string]any{"key": "value"})
camunda.NullVariable()
```

## Architecture

```
┌─────────────────────────────────────┐
│      Your Application               │
│                                     │
│  ┌──────────────────────────────┐  │
│  │   TaskHandler Implementation │  │
│  │   (Business Logic)           │  │
│  └──────────────────────────────┘  │
│              ▲                      │
│              │                      │
│  ┌───────────┴──────────────────┐  │
│  │   camunda.Worker             │  │
│  │   • Polling                  │  │
│  │   • Routing                  │  │
│  │   • Error Handling           │  │
│  └──────────────────────────────┘  │
│              ▲                      │
│              │                      │
│  ┌───────────┴──────────────────┐  │
│  │   camunda.Client             │  │
│  │   • HTTP API                 │  │
│  │   • Fluent Builders          │  │
│  └──────────────────────────────┘  │
└─────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│     Camunda Platform 7              │
│     (Process Engine + REST API)     │
└─────────────────────────────────────┘
```

## Development

### Run Tests

```bash
go test -v
```

### Run Benchmarks

```bash
go test -bench=. -benchmem
```

## License

See the main repository LICENSE file.