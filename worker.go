package camunda

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/nativebpm/httpstream"
)

// TopicRequest represents a topic request for fetching tasks
type TopicRequest struct {
	TopicName            string   `json:"topicName"`
	LockDuration         int      `json:"lockDuration"`
	Variables            []string `json:"variables,omitempty"`
	LocalVariables       bool     `json:"localVariables,omitempty"`
	BusinessKey          string   `json:"businessKey,omitempty"`
	ProcessDefinitionID  string   `json:"processDefinitionId,omitempty"`
	ProcessDefinitionKey string   `json:"processDefinitionKey,omitempty"`
	TenantIDs            []string `json:"tenantIds,omitempty"`
}

// ExternalTask represents a Camunda external task
type ExternalTask struct {
	ID                  string              `json:"id"`
	TopicName           string              `json:"topicName"`
	WorkerID            string              `json:"workerId"`
	LockExpirationTime  *time.Time          `json:"lockExpirationTime,omitempty"`
	Retries             *int                `json:"retries,omitempty"`
	ErrorMessage        string              `json:"errorMessage,omitempty"`
	ErrorDetails        string              `json:"errorDetails,omitempty"`
	Variables           map[string]Variable `json:"variables,omitempty"`
	BusinessKey         string              `json:"businessKey,omitempty"`
	TenantID            string              `json:"tenantId,omitempty"`
	Priority            int                 `json:"priority,omitempty"`
	ActivityID          string              `json:"activityId,omitempty"`
	ActivityInstanceID  string              `json:"activityInstanceId,omitempty"`
	ExecutionID         string              `json:"executionId,omitempty"`
	ProcessInstanceID   string              `json:"processInstanceId,omitempty"`
	ProcessDefinitionID string              `json:"processDefinitionId,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for ExternalTask
// to handle Camunda's timestamp format (e.g., "2025-10-08T03:50:45.087+0000")
func (t *ExternalTask) UnmarshalJSON(data []byte) error {
	// Use an alias type to avoid infinite recursion
	type Alias ExternalTask

	// Temporary struct with string for LockExpirationTime
	aux := &struct {
		LockExpirationTime *string `json:"lockExpirationTime,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(t),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	// Parse LockExpirationTime if present
	if aux.LockExpirationTime != nil && *aux.LockExpirationTime != "" {
		// Camunda format: "2025-10-08T03:50:45.087+0000"
		// Try multiple formats
		formats := []string{
			"2006-01-02T15:04:05.999-0700", // Camunda format with milliseconds
			"2006-01-02T15:04:05-0700",     // Camunda format without milliseconds
			time.RFC3339,                   // Standard RFC3339
			time.RFC3339Nano,               // RFC3339 with nanoseconds
		}

		var parsed time.Time
		var err error
		for _, format := range formats {
			parsed, err = time.Parse(format, *aux.LockExpirationTime)
			if err == nil {
				t.LockExpirationTime = &parsed
				break
			}
		}

		if err != nil {
			return fmt.Errorf("failed to parse lockExpirationTime %q: %w", *aux.LockExpirationTime, err)
		}
	}

	return nil
}

// CompleteFunc is a factory function that returns a preconfigured TaskCompletion
// so handlers can build variables fluently and then call Execute().
// Example: complete().StringVariable("ok", "yes").Execute()
type CompleteFunc func() *TaskCompletion

// FailFunc is a function to report a task failure
type FailFunc func(errorMessage, errorDetails string, retries, retryTimeout int) error

// Worker manages external task polling and processing
type Worker struct {
	client               *Client
	httpClient           *httpstream.Client
	workerID             string
	logger               *slog.Logger
	handlers             map[string]TaskHandler
	topics               []TopicRequest
	maxTasks             int
	pollInterval         time.Duration
	maxConcurrency       int            // Maximum number of concurrent task processors
	taskSemaphore        chan struct{}  // Semaphore to limit concurrent tasks
	activeTasksWg        sync.WaitGroup // Tracks active task goroutines
	asyncResponseTimeout int            // asyncResponseTimeout in milliseconds (long polling). 0 = disabled
}

// NewWorker creates a new external task worker
func NewWorker(client *Client, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	// Default concurrency: 2x maxTasks to allow for some buffering
	maxConcurrency := 20

	// Default: enable long polling 20s to reduce fetch/lock pressure
	defaultAsyncTimeout := 20000 // milliseconds

	return &Worker{
		client:               client,
		httpClient:           client.httpClient,
		workerID:             client.workerID,
		logger:               logger,
		handlers:             make(map[string]TaskHandler),
		topics:               []TopicRequest{},
		maxTasks:             10,
		pollInterval:         5 * time.Second,
		maxConcurrency:       maxConcurrency,
		taskSemaphore:        make(chan struct{}, maxConcurrency),
		asyncResponseTimeout: defaultAsyncTimeout,
	}
}

// SetAsyncResponseTimeout sets the asyncResponseTimeout (long polling) for fetchAndLock in milliseconds.
// Pass a time.Duration; a zero duration disables asyncResponseTimeout.
func (w *Worker) SetAsyncResponseTimeout(timeout time.Duration) *Worker {
	if timeout <= 0 {
		w.asyncResponseTimeout = 0
		return w
	}
	w.asyncResponseTimeout = int(timeout.Milliseconds())
	return w
}

// RegisterHandler registers a handler for a specific topic
func (w *Worker) RegisterHandler(topicName string, handler TaskHandler, lockDuration int, variables []string) *Worker {
	w.handlers[topicName] = handler
	w.topics = append(w.topics, TopicRequest{
		TopicName:    topicName,
		LockDuration: lockDuration,
		Variables:    variables,
	})
	w.logger.Info("Registered handler", "topic", topicName, "lockDuration", lockDuration)
	return w
}

// SetMaxTasks sets the maximum number of tasks to fetch per poll
func (w *Worker) SetMaxTasks(maxTasks int) *Worker {
	w.maxTasks = maxTasks
	return w
}

// SetPollInterval sets the interval between polls when no tasks are available
func (w *Worker) SetPollInterval(interval time.Duration) *Worker {
	w.pollInterval = interval
	return w
}

// SetMaxConcurrency sets the maximum number of tasks processed concurrently
func (w *Worker) SetMaxConcurrency(maxConcurrency int) *Worker {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	w.maxConcurrency = maxConcurrency
	w.taskSemaphore = make(chan struct{}, maxConcurrency)
	return w
}

// Start begins polling for external tasks
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting external task worker",
		"topics", len(w.topics),
		"maxTasks", w.maxTasks,
		"maxConcurrency", w.maxConcurrency)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Context canceled, waiting for active tasks to complete...")
			w.activeTasksWg.Wait()
			w.logger.Info("Worker stopped gracefully")
			return
		case <-ticker.C:
			tasks, err := w.fetchAndLock(ctx)
			if err != nil {
				w.logger.Error("Failed to fetch tasks", "error", err)
				continue
			}

			if len(tasks) == 0 {
				continue
			}

			w.logger.Info("Fetched tasks", "count", len(tasks))

			for _, task := range tasks {
				w.taskSemaphore <- struct{}{}
				w.activeTasksWg.Add(1)
				go w.processTaskSafe(ctx, task)
			}
		}
	}
}

// fetchAndLock fetches and locks external tasks
func (w *Worker) fetchAndLock(ctx context.Context) ([]ExternalTask, error) {
	req := struct {
		WorkerID    string         `json:"workerId"`
		MaxTasks    int            `json:"maxTasks"`
		UsePriority bool           `json:"usePriority"`
		Topics      []TopicRequest `json:"topics"`
		// AsyncResponseTimeout enables long polling on the REST API. Omit when 0.
		AsyncResponseTimeout *int `json:"asyncResponseTimeout,omitempty"`
	}{
		WorkerID:    w.workerID,
		MaxTasks:    w.maxTasks,
		UsePriority: true,
		Topics:      w.topics,
	}

	if w.asyncResponseTimeout > 0 {
		val := w.asyncResponseTimeout
		req.AsyncResponseTimeout = &val
	}

	resp, err := w.httpClient.POST(ctx, "/external-task/fetchAndLock").
		JSON(req).
		Send()
	if err != nil {
		return nil, fmt.Errorf("failed to send fetchAndLock request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetchAndLock request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tasks []ExternalTask
	if err := json.Unmarshal(body, &tasks); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tasks: %w", err)
	}

	return tasks, nil
}

// processTaskSafe wraps processTask with panic recovery and cleanup.
// This method is called from goroutines and handles all concurrency concerns:
// - Semaphore slot release
// - WaitGroup tracking
// - Panic recovery with Camunda failure reporting
// Use this method for production goroutine execution.
func (w *Worker) processTaskSafe(ctx context.Context, task ExternalTask) {
	// Ensure cleanup happens regardless of panic or normal return
	defer func() {
		// Release semaphore slot
		<-w.taskSemaphore

		// Mark task as done in WaitGroup
		w.activeTasksWg.Done()

		// Recover from panic to prevent goroutine crash
		if r := recover(); r != nil {
			w.logger.Error("Panic in task processing",
				"taskID", task.ID,
				"topic", task.TopicName,
				"panic", r,
				"stack", string(debug.Stack()))

			// Try to report failure to Camunda
			failErr := NewTaskFailure(w.httpClient, w.workerID, task.ID).
				Context(ctx).
				ErrorMessage("Task handler panicked").
				ErrorDetails(fmt.Sprintf("Panic: %v\n\nStack:\n%s", r, debug.Stack())).
				Retries(3).
				RetryTimeout(60000).
				Execute()

			if failErr != nil {
				w.logger.Error("Failed to report panic to Camunda",
					"taskID", task.ID,
					"error", failErr)
			}
		}
	}()

	// Process the task
	w.processTask(ctx, task)
}

// processTask processes a single task using the registered handler.
// This method contains the core business logic without concurrency management,
// making it easier to test. For production use, call processTaskSafe instead.
func (w *Worker) processTask(ctx context.Context, task ExternalTask) {
	handler, ok := w.handlers[task.TopicName]
	if !ok {
		w.logger.Error("No handler registered for topic", "topic", task.TopicName, "taskID", task.ID)
		return
	}

	complete := func() *TaskCompletion {
		return NewTaskCompletion(w.httpClient, w.workerID, task.ID).Context(ctx).Logger(w.logger)
	}

	// Create fail function
	fail := func(errorMessage, errorDetails string, retries, retryTimeout int) error {
		return NewTaskFailure(w.httpClient, w.workerID, task.ID).
			Context(ctx).
			ErrorMessage(errorMessage).
			ErrorDetails(errorDetails).
			Retries(retries).
			RetryTimeout(retryTimeout).
			Execute()
	}

	// Handler is responsible for logging and error handling
	if err := handler.Handle(ctx, w.client, task, complete, fail); err != nil {
		w.logger.Error("task handler returned error",
			"taskID", task.ID,
			"error", err)

		// Try to report failure to Camunda so the engine records the error and retries if configured
		if failErr := fail(err.Error(), "handler error", 0, 0); failErr != nil {
			w.logger.Error("failed to report task failure to Camunda",
				"taskID", task.ID,
				"reportError", failErr)
		}
	}
}
