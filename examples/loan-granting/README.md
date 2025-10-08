# Loan Granting External Task Worker Example

This example demonstrates a clean architecture for implementing Camunda External Task Workers in Go, following best practices for separation of concerns.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         Camunda Platform                         │
│                    (Process Engine + REST API)                   │
└─────────────────────┬───────────────────────────────────────────┘
                      │ External Task API
                      │ (FetchAndLock, Complete, Failure)
                      │
┌─────────────────────▼───────────────────────────────────────────┐
│                           main.go                                │
│  • Initializes Camunda client                                   │
│  • Deploys BPMN process                                         │
│  • Creates and configures worker                                │
│  • Handles graceful shutdown                                    │
└─────────────────────┬───────────────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────────────┐
│                  camunda.Worker (from SDK)                       │
│  • Polls for external tasks (FetchAndLock)                      │
│  • Routes tasks to handlers by topic name                       │
│  • Manages concurrent task processing                           │
│  • Handles errors and reports failures                          │
└─────────┬──────────────┬──────────────┬─────────────────────────┘
          │              │              │
          │              │              │
┌─────────▼─────┐ ┌─────▼──────┐ ┌────▼────────┐
│ handlers/      │ │ handlers/  │ │ handlers/   │
│ credit_score_  │ │ loan_      │ │ request_    │
│ checker.go     │ │ granter.go │ │ rejecter.go │
│                │ │            │ │             │
│ • Verifies     │ │ • Approves │ │ • Rejects   │
│   credit score │ │   loans    │ │   requests  │
│ • Returns      │ │ • Calculates│ │ • Provides  │
│   scores array │ │   amount   │ │   reason    │
└────────────────┘ └────────────┘ └─────────────┘
```

## Components

The application is structured into two main layers:

### 1. **SDK Layer** (`camunda` package)
The Camunda connector provides:
- **`camunda.Client`** - HTTP client for Camunda REST API
- **`camunda.Worker`** - External task polling and routing infrastructure
- **`camunda.TaskHandler`** - Interface for implementing task handlers

### 2. **Application Layer**

#### **Handlers** (`handlers/`)
Individual task handlers that implement business logic for specific topics:

- **`credit_score_checker.go`** - Handles credit score verification tasks
  - Topic: `creditScoreChecker`
  - Returns an array of credit scores
  
- **`loan_granter.go`** - Processes loan approval for good credit scores
  - Topic: `loanGranter`
  - Calculates and grants loan amounts
  
- **`request_rejecter.go`** - Handles loan rejection for low credit scores
  - Topic: `requestRejecter`
  - Provides rejection reasons

Each handler implements the `camunda.TaskHandler` interface:
```go
type TaskHandler interface {
    Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask) error
}
```

#### **Main** (`main.go`)
Application entry point that:
- Initializes the Camunda client
- Deploys BPMN process definitions
- Registers all task handlers
- Manages graceful shutdown

## Running the Example

### Prerequisites
- Camunda Platform 7 running on `http://localhost:8080`
- Go 1.21 or later

### Start the worker

```bash
cd camunda/examples/loan-granting
go run main.go
```

### Expected Output

```bash
2025/10/08 00:00:00 INFO Deployed BPMN process deploymentID=abc123
2025/10/08 00:00:00 INFO Started test process instance id=def456
2025/10/08 00:00:00 INFO Worker configured topics=3 maxTasks=10 pollInterval=5s
2025/10/08 00:00:00 INFO Starting external task worker... Press Ctrl+C to stop
2025/10/08 00:00:00 INFO Starting external task worker topics=3 maxTasks=10
2025/10/08 00:00:01 INFO Fetched tasks count=1
2025/10/08 00:00:01 INFO Processing task taskID=task-1 topic=creditScoreChecker
2025/10/08 00:00:02 INFO Task processed successfully taskID=task-1
```

**Note:** The program will appear to "hang" after `"Starting external task worker..."` - **this is normal behavior!** The worker runs in a blocking loop, continuously polling for tasks. Press `Ctrl+C` to stop gracefully.

### Expected Flow

1. **Deployment**: BPMN process is deployed to Camunda
2. **Process Start**: A test process instance is created
3. **Worker Polling**: The worker starts polling for external tasks (blocking operation)
4. **Task Processing**:
   - `creditScoreChecker` calculates credit scores
   - Based on score, either `loanGranter` or `requestRejecter` is executed
5. **Graceful Shutdown**: Press `Ctrl+C` to stop the worker

The worker continues running until you stop it with `Ctrl+C` or send a termination signal. This is the expected behavior for a long-running service.

## Benefits of This Architecture

### ✅ Separation of Concerns
- Business logic is isolated in handlers
- Worker focuses on task orchestration
- Main function handles initialization

### ✅ Testability
Each handler can be unit tested independently:
```go
func TestCreditScoreChecker(t *testing.T) {
    handler := handlers.NewCreditScoreChecker(slog.Default())
    // Mock client and task
    err := handler.Handle(ctx, client, task)
    // Assert results
}
```

### ✅ Maintainability
- Easy to add new topics/handlers
- Clear error handling strategy
- Self-documenting code structure

### ✅ Scalability
- Handlers can be moved to separate services
- Worker can be horizontally scaled
- Concurrent task processing

## Using the Camunda Worker

The worker is now part of the core `camunda` package and can be used in any application:

```go
// Create Camunda client
client, _ := camunda.NewClient("http://localhost:8080", "my-worker")

// Create worker
worker := camunda.NewWorker(client, logger)

// Register handlers
worker.RegisterHandler("myTopic", myHandler, 60000, []string{"var1"})

// Configure
worker.SetMaxTasks(10).SetPollInterval(5 * time.Second)

// Start (blocking call - runs until context is cancelled)
worker.Start(ctx)
```

### ⚠️ Important: Worker Lifecycle

`worker.Start(ctx)` is a **blocking call** - it runs an infinite polling loop until the context is cancelled. This is by design!

For detailed information about worker lifecycle, graceful shutdown, and best practices, see: [../../WORKER_LIFECYCLE.md](../../WORKER_LIFECYCLE.md)

## Adding New Handlers

1. Create a new handler implementing `camunda.TaskHandler`:
```go
type MyNewHandler struct {
    logger *slog.Logger
}

func (h *MyNewHandler) Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask) error {
    // Your business logic here
    return nil
}
```

2. Register it in `main.go`:
```go
myHandler := handlers.NewMyNewHandler(logger)
w.RegisterHandler("myTopicName", myHandler, 60000, []string{"var1", "var2"})
```

## Configuration

Worker configuration can be adjusted:
```go
w := worker.NewWorker(client, logger)
w.SetMaxTasks(10)  // Maximum tasks per poll
```

Handler registration with lock duration and variables:
```go
w.RegisterHandler(
    "topicName",           // Topic to subscribe to
    handler,               // Handler implementation
    60000,                 // Lock duration in milliseconds
    []string{"var1"},      // Variables to fetch
)
```

## Error Handling

The worker automatically:
- Catches handler errors
- Reports failures to Camunda
- Sets retry count and timeout
- Logs all operations

Handlers should return errors for recoverable failures:
```go
if err != nil {
    return fmt.Errorf("failed to process: %w", err)
}
```

## Process Definition

The BPMN process (`bpmn/loan-granting.bpmn`) defines:
- External task topics
- Process flow and gateways
- Variable mappings

Make sure your handler topic names match the external task definitions in the BPMN.
