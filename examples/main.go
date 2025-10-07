package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/nativebpm/camunda"
)

func main() {
	logger := slog.Default()

	// Create a new Camunda client
	client, err := camunda.NewClient("http://localhost:8080/engine-rest", "my-worker")
	if err != nil {
		logger.Error("Failed to create client", "error", err)
		return
	}

	// Add logging middleware using fluent API
	client.WithLogger(logger)

	// Define topics to subscribe to
	topics := []camunda.TopicRequest{
		{
			TopicName:    "invoice-processing",
			LockDuration: 60000, // 1 minute
		},
	}

	ctx := context.Background()

	// Poll for tasks in a loop
	for {
		tasks, err := client.FetchAndLock(ctx, topics, 10, nil)
		if err != nil {
			logger.Error("Failed to fetch tasks", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(tasks) == 0 {
			logger.Info("No tasks available, waiting...")
			time.Sleep(5 * time.Second)
			continue
		}

		// Process each task
		for _, task := range tasks {
			go processTask(client, task, logger)
		}

		// Wait a bit before next poll
		time.Sleep(1 * time.Second)
	}
}

func processTask(client *camunda.Client, task camunda.ExternalTask, logger *slog.Logger) {
	ctx := context.Background()
	logger.Info("Processing task", "taskID", task.ID)

	// Simulate processing
	time.Sleep(2 * time.Second)

	// Complete the task
	variables := map[string]camunda.Variable{
		"processed": camunda.BooleanVariable(true),
		"result":    camunda.StringVariable("completed"),
		"count":     camunda.IntVariable(42),
	}

	err := client.Complete(ctx, task.ID, variables, nil)
	if err != nil {
		logger.Error("Failed to complete task", "taskID", task.ID, "error", err)
		// Handle failure
		err := client.HandleFailure(ctx, task.ID, "Processing failed", "Detailed error message", 3, 30000)
		if err != nil {
			logger.Error("Failed to handle failure for task", "taskID", task.ID, "error", err)
		}
	} else {
		logger.Info("Completed task", "taskID", task.ID)
	}
}
