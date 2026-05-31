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
- **Concurrent processing** - Controlled parallel task execution with semaphore
- **Graceful shutdown** - Context-aware cancellation with WaitGroup
- **Panic recovery** - Automatic recovery and error reporting
- **Configurable** - Adjust max tasks, poll interval, concurrency limits, etc.

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
    worker.SetMaxTasks(10).
        SetPollInterval(5 * time.Second).
        SetMaxConcurrency(5) // Control parallel task execution
    
    worker.Start(context.Background())
}
```

## Examples

Check out the [examples](./examples) directory for complete working examples:

- **[loan-granting](./examples/loan-granting)** - Complete external task worker with BPMN deployment, process start, handler-based architecture, and concurrency control

### Concurrency Control Example

```go
import "runtime"

// Example 1: I/O-intensive tasks (HTTP calls, database queries)
worker := camunda.NewWorker(client, logger).
    SetMaxTasks(20).
    SetMaxConcurrency(30). // Higher for I/O wait
    RegisterHandler("sendEmail", emailHandler, 60000, nil)

// Example 2: CPU-intensive tasks (image processing, calculations)
worker := camunda.NewWorker(client, logger).
    SetMaxTasks(10).
    SetMaxConcurrency(runtime.NumCPU()). // Match CPU cores
    RegisterHandler("processImage", imageHandler, 300000, nil)

// Example 3: Memory-intensive tasks (PDF generation, large payloads)
worker := camunda.NewWorker(client, logger).
    SetMaxTasks(5).
    SetMaxConcurrency(2). // Conservative limit
    RegisterHandler("generatePDF", pdfHandler, 600000, nil)
```

### Graceful Shutdown Example

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Handle OS signals
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

go func() {
    <-sigChan
    logger.Info("Shutdown signal received, stopping worker...")
    cancel() // Worker will wait for active tasks to complete
}()

// Start worker (blocking call)
worker.Start(ctx)

logger.Info("All tasks completed, exiting gracefully")
```

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
worker.SetMaxConcurrency(20)               // Max parallel task processing (default: 20)
```

**Concurrency Guidelines:**
- **Default**: `20` (2x maxTasks) - balanced for mixed workloads
- **I/O-intensive** (HTTP requests, DB queries): `20-50` - higher concurrency benefits from I/O wait time
- **CPU-intensive** (image processing, calculations): `runtime.NumCPU()` - match available CPU cores
- **Memory-intensive** (PDF generation, large data): `1-5` - prevent memory exhaustion

**How it works:**
- Uses a **semaphore** (buffered channel) to limit concurrent goroutines
- **WaitGroup** ensures graceful shutdown - waits for active tasks to complete
- **Panic recovery** prevents goroutine crashes and reports failures to Camunda
- All task goroutines respect context cancellation

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

### Component Architecture

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
│  │   • Concurrency Control      │  │
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

### Concurrency Architecture

```
Worker Polling Loop:
┌────────────────────────────────────────────────────────┐
│  1. Fetch & Lock (max 10 tasks)                       │
│     GET /external-task/fetchAndLock                   │
└────────────────────────────────────────────────────────┘
                        │
                        ▼
┌────────────────────────────────────────────────────────┐
│  2. For each task:                                     │
│     • Acquire semaphore slot (blocks if limit reached) │
│     • Add to WaitGroup                                 │
│     • Launch goroutine with panic recovery             │
└────────────────────────────────────────────────────────┘
                        │
        ┌───────────────┼───────────────┐
        ▼               ▼               ▼
┌─────────────┐ ┌─────────────┐ ┌─────────────┐
│ Goroutine 1 │ │ Goroutine 2 │ │ Goroutine N │
│ Task ID 123 │ │ Task ID 456 │ │ Task ID ... │
│             │ │             │ │             │
│ ┌─────────┐ │ │ ┌─────────┐ │ │ ┌─────────┐ │
│ │ Handler │ │ │ │ Handler │ │ │ │ Handler │ │
│ │  Logic  │ │ │ │  Logic  │ │ │ │  Logic  │ │
│ └─────────┘ │ │ └─────────┘ │ │ └─────────┘ │
│             │ │             │ │             │
│ Complete or │ │ Complete or │ │ Complete or │
│ Fail Task   │ │ Fail Task   │ │ Fail Task   │
└─────────────┘ └─────────────┘ └─────────────┘
        │               │               │
        └───────────────┼───────────────┘
                        ▼
┌────────────────────────────────────────────────────────┐
│  3. Cleanup:                                           │
│     • Release semaphore slot                           │
│     • WaitGroup.Done()                                 │
│     • Panic recovery (if needed)                       │
└────────────────────────────────────────────────────────┘
                        │
                        ▼
┌────────────────────────────────────────────────────────┐
│  4. On ctx.Done():                                     │
│     • Stop fetching new tasks                          │
│     • WaitGroup.Wait() - wait for active tasks        │
│     • Exit gracefully                                  │
└────────────────────────────────────────────────────────┘

Semaphore Control (maxConcurrency = 5):
┌────────────────────────────┐
│  taskSemaphore (buffered)  │
│  ┌───┬───┬───┬───┬───┐    │
│  │ ✓ │ ✓ │ ✓ │ ✓ │   │    │  Active: 4/5
│  └───┴───┴───┴───┴───┘    │
└────────────────────────────┘
   ▲                    │
   │ acquire            │ release
   │                    ▼
Task goroutine lifecycle
```

## Advanced Topics

### Goroutine Management

The worker uses a sophisticated goroutine management system:

1. **Semaphore Pattern** - Buffered channel limits concurrent task processing
2. **WaitGroup Tracking** - Ensures all tasks complete before shutdown
3. **Panic Recovery** - Catches panics, logs stack traces, reports to Camunda
4. **Context Awareness** - All goroutines respect context cancellation

**Benefits:**
- **Resource control** - Prevent memory/CPU exhaustion
- **Predictable behavior** - No unbounded goroutine creation
- **Graceful shutdown** - No lost tasks during shutdown
- **Fault tolerance** - Panics don't crash the worker

For detailed information, see [GOROUTINE_IMPROVEMENTS.md](./GOROUTINE_IMPROVEMENTS.md)

### Error Handling

The worker automatically handles errors in three ways:

1. **Handler returns error** → Reports failure to Camunda with retries
2. **Handler panics** → Recovers, logs stack trace, reports failure
3. **Context cancelled** → Stops fetching, waits for active tasks, exits gracefully

Example error handling in handler:

```go
func (h *MyHandler) Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask) error {
    // Validation error - will be reported to Camunda
    if task.Variables["amount"].Value == 0 {
        return fmt.Errorf("invalid amount: %v", task.Variables["amount"])
    }
    
    // Business logic that might panic - will be caught and reported
    result := h.processTask(task)
    
    // Complete task
    return client.Complete(task.ID).
        Context(ctx).
        Variable("result", camunda.StringVariable(result)).
        Execute()
}
```

### Performance Tuning

#### Finding Optimal Settings

1. **Start conservative**:
   ```go
   worker.SetMaxTasks(5).SetMaxConcurrency(5)
   ```

2. **Monitor metrics**:
   - Task processing time
   - Memory usage
   - CPU utilization
   - Queue depth (tasks waiting)

3. **Adjust based on workload**:
   - **High queue depth** → Increase `maxConcurrency`
   - **High memory** → Decrease `maxConcurrency`
   - **High CPU (close to 100%)** → Decrease for CPU-bound, increase for I/O-bound
   - **Low utilization** → Increase `maxTasks` to fetch more work

#### Typical Configurations

| Workload Type | MaxTasks | MaxConcurrency | Example |
|--------------|----------|----------------|---------|
| **Light I/O** | 10 | 20-30 | Email sending, simple API calls |
| **Heavy I/O** | 20 | 30-50 | Multiple DB queries, external services |
| **Light CPU** | 10 | NumCPU × 2 | JSON parsing, text processing |
| **Heavy CPU** | 5 | NumCPU | Image processing, video encoding |
| **Memory-intensive** | 5 | 1-3 | Large file processing, PDF generation |
| **Mixed** | 10 | 20 | Typical business applications |

### Job Executor Smart Configuration

When running many jobs in Camunda, tune the job executor to avoid expensive global operations and unnecessary sorting.

- Enable job acquisition throttling to reduce contention and smooth load:
    - queueSize (how many jobs to queue locally)
    - maxJobsPerAcquisition (limit how many jobs the executor grabs in one acquisition)

- Avoid setting `jobExecutorAcquireByDueDate=true` when the system has a large number of jobs – it causes sorting of all available jobs which can be expensive. Prefer acquisition by id or partitioning.

- Consider running multiple job executors with filters (different `jobExecutor` configurations or process/job priorities) to separate workloads (e.g., short-running vs long-running jobs).

These settings are on the Camunda engine side (process-engine configuration) and are orthogonal to the external task worker. The worker should focus on reasonable `maxTasks` (10–50) and `asyncResponseTimeout` (long polling) to reduce REST API pressure.

## Development

### Run Tests

```bash
go test -v
```

### Run Benchmarks

```bash
go test -bench=. -benchmem
```

### Build Example

```bash
cd examples/loan-granting
go build
```

## Documentation

- [GOROUTINE_IMPROVEMENTS.md](./GOROUTINE_IMPROVEMENTS.md) - Detailed concurrency implementation
- [JSON_VARIABLE_FIX.md](./JSON_VARIABLE_FIX.md) - Variable type handling
- [Examples](./examples) - Working code examples

## License

See the main repository LICENSE file.