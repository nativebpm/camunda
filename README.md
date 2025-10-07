# Camunda External Task Client

A Go client for Camunda 7 external tasks.

## Features

- Fetch and lock external tasks
- Complete tasks with variables
- Handle task failures
- Extend task locks
- Unlock tasks
- Fluent API with middleware support
- Structured logging with slog
- Type-safe variable handling

## Variable Types

The client provides type-safe constructors for Camunda variables:

- `StringVariable(value)` - String variables
- `IntVariable(value)` / `LongVariable(value)` - Integer variables
- `DoubleVariable(value)` - Float variables
- `BooleanVariable(value)` - Boolean variables
- `DateVariable(value)` - Date/time variables
- `JSONVariable(value)` - JSON object variables
- `NullVariable()` - Null variables

## Installation

```bash
go get github.com/nativebpm/camunda
```

## Usage

```go
package main

import (
    "context"
    "log/slog"

    "github.com/nativebpm/camunda"
)

func main() {
    // Create client
    client, err := camunda.NewClient("http://localhost:8080/engine-rest", "my-worker")
    if err != nil {
        log.Fatal(err)
    }

    // Add logging middleware using fluent API
    logger := slog.Default()
    client.WithLogger(logger)

    // Define topics
    topics := []camunda.TopicRequest{
        {
            TopicName:    "my-topic",
            LockDuration: 60000, // 1 minute
        },
    }

    // Fetch tasks
    tasks, err := client.FetchAndLock(context.Background(), topics, 10, nil)
    if err != nil {
        log.Fatal(err)
    }

    // Process tasks
    for _, task := range tasks {
        // Complete task with type-safe variables
        variables := map[string]camunda.Variable{
            "result":    camunda.StringVariable("done"),
            "count":     camunda.IntVariable(42),
            "processed": camunda.BooleanVariable(true),
            "data":      camunda.JSONVariable(map[string]any{"key": "value"}),
        }
        err := client.Complete(context.Background(), task.ID, variables, nil)
        if err != nil {
            log.Printf("Failed to complete task: %v", err)
        }
    }
}
```

## API

### Client Methods

- `NewClient(baseURL, workerID)` - Create a new client
- `FetchAndLock(ctx, topics, maxTasks, asyncTimeout)` - Fetch and lock tasks
- `Complete(ctx, taskID, variables, localVariables)` - Complete a task
- `HandleFailure(ctx, taskID, errorMessage, errorDetails, retries, retryTimeout)` - Handle task failure
- `ExtendLock(ctx, taskID, newDuration)` - Extend task lock
- `Unlock(ctx, taskID)` - Unlock a task

## Running the Example

```bash
cd examples
go run main.go
```

Make sure Camunda is running on `http://localhost:8080`.