package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nativebpm/camunda/internal/builder"
	"github.com/nativebpm/connectors/httpclient"
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
	ID                  string                      `json:"id"`
	TopicName           string                      `json:"topicName"`
	WorkerID            string                      `json:"workerId"`
	LockExpirationTime  *time.Time                  `json:"lockExpirationTime,omitempty"`
	Retries             *int                        `json:"retries,omitempty"`
	ErrorMessage        string                      `json:"errorMessage,omitempty"`
	ErrorDetails        string                      `json:"errorDetails,omitempty"`
	Variables           map[string]builder.Variable `json:"variables,omitempty"`
	BusinessKey         string                      `json:"businessKey,omitempty"`
	TenantID            string                      `json:"tenantId,omitempty"`
	Priority            int                         `json:"priority,omitempty"`
	ActivityID          string                      `json:"activityId,omitempty"`
	ActivityInstanceID  string                      `json:"activityInstanceId,omitempty"`
	ExecutionID         string                      `json:"executionId,omitempty"`
	ProcessInstanceID   string                      `json:"processInstanceId,omitempty"`
	ProcessDefinitionID string                      `json:"processDefinitionId,omitempty"`
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

// TaskHandler defines the interface for external task handlers
type TaskHandler interface {
	Handle(ctx context.Context, task ExternalTask, complete CompleteFunc, fail FailFunc) error
}

// CompleteFunc is a function to complete a task
type CompleteFunc func(vars map[string]builder.Variable) error

// FailFunc is a function to report a task failure
type FailFunc func(errorMessage, errorDetails string, retries, retryTimeout int) error

// Worker manages external task polling and processing
type Worker struct {
	httpClient   *httpclient.HTTPClient
	workerID     string
	logger       *slog.Logger
	handlers     map[string]TaskHandler
	topics       []TopicRequest
	maxTasks     int
	pollInterval time.Duration
}

// New creates a new external task worker
func New(httpClient *httpclient.HTTPClient, workerID string, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		httpClient:   httpClient,
		workerID:     workerID,
		logger:       logger,
		handlers:     make(map[string]TaskHandler),
		topics:       []TopicRequest{},
		maxTasks:     10,
		pollInterval: 5 * time.Second,
	}
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

// Start begins polling for external tasks
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting external task worker", "topics", len(w.topics), "maxTasks", w.maxTasks)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Worker stopped")
			return
		default:
		}

		tasks, err := w.fetchAndLock(ctx)
		if err != nil {
			w.logger.Error("Failed to fetch tasks", "error", err)
			time.Sleep(w.pollInterval)
			continue
		}

		if len(tasks) == 0 {
			time.Sleep(w.pollInterval)
			continue
		}

		w.logger.Info("Fetched tasks", "count", len(tasks))

		// Process each task in a separate goroutine
		for _, task := range tasks {
			go w.processTask(ctx, task)
		}

		// Brief pause before next poll
		time.Sleep(1 * time.Second)
	}
}

// fetchAndLock fetches and locks external tasks
func (w *Worker) fetchAndLock(ctx context.Context) ([]ExternalTask, error) {
	req := struct {
		WorkerID    string         `json:"workerId"`
		MaxTasks    int            `json:"maxTasks"`
		UsePriority bool           `json:"usePriority"`
		Topics      []TopicRequest `json:"topics"`
	}{
		WorkerID:    w.workerID,
		MaxTasks:    w.maxTasks,
		UsePriority: true,
		Topics:      w.topics,
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

// processTask processes a single task using the registered handler
func (w *Worker) processTask(ctx context.Context, task ExternalTask) {
	handler, ok := w.handlers[task.TopicName]
	if !ok {
		w.logger.Error("No handler registered for topic", "topic", task.TopicName, "taskID", task.ID)
		return
	}

	// Create complete function
	complete := func(vars map[string]builder.Variable) error {
		return builder.NewTaskCompletion(w.httpClient, w.workerID, task.ID).
			Context(ctx).
			Variables(vars).
			Execute()
	}

	// Create fail function
	fail := func(errorMessage, errorDetails string, retries, retryTimeout int) error {
		return builder.NewTaskFailure(w.httpClient, w.workerID, task.ID).
			Context(ctx).
			ErrorMessage(errorMessage).
			ErrorDetails(errorDetails).
			Retries(retries).
			RetryTimeout(retryTimeout).
			Execute()
	}

	// Handler is responsible for logging and error handling
	_ = handler.Handle(ctx, task, complete, fail)
}
