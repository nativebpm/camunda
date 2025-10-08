package camunda

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nativebpm/camunda/internal/builder"
	"github.com/nativebpm/camunda/internal/worker"
	"github.com/nativebpm/connectors/httpclient"
)

// ExternalTask represents a Camunda external task
type ExternalTask = worker.ExternalTask

// Variable represents a Camunda variable with type safety
type Variable = builder.Variable

// TopicRequest represents a topic request for fetching tasks
type TopicRequest = worker.TopicRequest

// StringVariable creates a string variable
func StringVariable(value string) Variable {
	return Variable{
		Value: value,
		Type:  "String",
	}
}

// IntVariable creates an integer variable
func IntVariable(value int64) Variable {
	return Variable{
		Value: value,
		Type:  "Integer",
	}
}

// LongVariable creates a long variable
func LongVariable(value int64) Variable {
	return Variable{
		Value: value,
		Type:  "Long",
	}
}

// DoubleVariable creates a double variable
func DoubleVariable(value float64) Variable {
	return Variable{
		Value: value,
		Type:  "Double",
	}
}

// BooleanVariable creates a boolean variable
func BooleanVariable(value bool) Variable {
	return Variable{
		Value: value,
		Type:  "Boolean",
	}
}

// DateVariable creates a date variable
func DateVariable(value time.Time) Variable {
	return Variable{
		Value: value.Format(time.RFC3339),
		Type:  "Date",
	}
}

// JSONVariable creates a JSON variable from any value
// The value is serialized to a JSON string and stored as a Camunda Object type
// This allows the JSON to be accessed in BPMN expressions
func JSONVariable(value any) Variable {
	// Serialize value to JSON string
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		// If marshaling fails, return the error as a string value
		// This allows the caller to see what went wrong
		return Variable{
			Value: fmt.Sprintf("ERROR: failed to marshal JSON: %v", err),
			Type:  "String",
		}
	}

	return Variable{
		Value: string(jsonBytes),
		Type:  "Object",
		ValueInfo: map[string]any{
			"objectTypeName":          "java.util.LinkedHashMap",
			"serializationDataFormat": "application/json",
		},
	}
}

// ListVariable creates a list variable from a slice
// This is used for multi-instance activities in BPMN where Camunda needs to iterate over a collection
// The value must be a slice ([]int, []string, []any, etc.)
func ListVariable(value any) Variable {
	// Serialize value to JSON string (required by Camunda for Object type)
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return Variable{
			Value: fmt.Sprintf("ERROR: failed to marshal list: %v", err),
			Type:  "String",
		}
	}

	return Variable{
		Value: string(jsonBytes),
		Type:  "Object",
		ValueInfo: map[string]any{
			"objectTypeName":          "java.util.ArrayList",
			"serializationDataFormat": "application/json",
		},
	}
}

// NullVariable creates a null variable
func NullVariable() Variable {
	return Variable{
		Value: nil,
		Type:  "Null",
	}
}

// Client represents a Camunda external task client
type Client struct {
	httpClient *httpclient.HTTPClient
	workerID   string
}

// NewClient creates a new Camunda external task client
func NewClient(hostURL, workerID string) (*Client, error) {
	baseURL := hostURL + "/engine-rest"
	httpClient, err := httpclient.NewClient(http.Client{Timeout: 30 * time.Second}, baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return &Client{
		httpClient: httpClient,
		workerID:   workerID,
	}, nil
}

// Use adds middleware to the HTTP client
func (c *Client) Use(middleware httpclient.Middleware) *Client {
	c.httpClient.Use(middleware)
	return c
}

// WithLogger adds logging middleware to the HTTP client
func (c *Client) WithLogger(logger *slog.Logger) *Client {
	c.httpClient.WithLogger(logger)
	return c
}

// TaskCompletion provides a fluent API for completing external tasks
type TaskCompletion = builder.TaskCompletion

// Complete creates a new TaskCompletion builder
func (c *Client) Complete(taskID string) *TaskCompletion {
	return builder.NewTaskCompletion(c.httpClient, c.workerID, taskID)
}

// TaskFailure provides a fluent API for reporting task failures
type TaskFailure = builder.TaskFailure

// Failure creates a new TaskFailure builder
func (c *Client) Failure(taskID string) *TaskFailure {
	return builder.NewTaskFailure(c.httpClient, c.workerID, taskID)
}

// LockExtension provides a fluent API for extending task locks
type LockExtension = builder.LockExtension

// ExtendLock creates a new LockExtension builder
func (c *Client) ExtendLock(taskID string, newDuration int) *LockExtension {
	return builder.NewLockExtension(c.httpClient, c.workerID, taskID, newDuration)
}

// TaskUnlock provides a fluent API for unlocking tasks
type TaskUnlock = builder.TaskUnlock

// Unlock creates a new TaskUnlock builder
func (c *Client) Unlock(taskID string) *TaskUnlock {
	return builder.NewTaskUnlock(c.httpClient, c.workerID, taskID)
}

// StartProcessInstance starts a new process instance by process definition key
func (c *Client) StartProcessInstance(ctx context.Context, processDefinitionKey string, variables map[string]any) (string, error) {
	// Prepare the request payload
	payload := map[string]any{
		"variables": make(map[string]map[string]any),
	}

	for key, value := range variables {
		payload["variables"].(map[string]map[string]any)[key] = map[string]any{
			"value": value,
		}
	}

	resp, err := c.httpClient.POST(ctx, "/process-definition/key/{processDefinitionKey}/start").
		PathParam("processDefinitionKey", processDefinitionKey).
		JSON(payload).
		Send()
	if err != nil {
		return "", fmt.Errorf("failed to send start process request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("start process request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal process instance: %w", err)
	}

	return result.ID, nil
}

// DeployProcess deploys a BPMN process definition to Camunda
func (c *Client) DeployProcess(ctx context.Context, deploymentName string, bpmnReader io.Reader, filename string) (string, error) {
	resp, err := c.httpClient.Multipart(ctx, "/deployment/create").
		Param("deployment-name", deploymentName).
		Param("enable-duplicate-filtering", "true").
		File("data", filename, bpmnReader).
		Send()
	if err != nil {
		return "", fmt.Errorf("failed to send deploy request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deploy request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal deployment: %w", err)
	}

	return result.ID, nil
}

// TaskHandler defines the interface for external task handlers
// Handlers implement business logic for specific topics
type TaskHandler interface {
	Handle(ctx context.Context, client *Client, task ExternalTask) error
}

// Worker manages external task polling and processing with a clean handler-based architecture
type Worker struct {
	internalWorker *worker.Worker
	client         *Client
	logger         *slog.Logger
}

// NewWorker creates a new external task worker
func NewWorker(client *Client, logger *slog.Logger) *Worker {
	return &Worker{
		internalWorker: worker.New(client.httpClient, client.workerID, logger),
		client:         client,
		logger:         logger,
	}
}

// RegisterHandler registers a handler for a specific topic
// Returns the worker for method chaining
func (w *Worker) RegisterHandler(topicName string, handler TaskHandler, lockDuration int, variables []string) *Worker {
	// Wrap the public handler interface to match internal interface
	internalHandler := &handlerAdapter{
		handler: handler,
		client:  w.client,
		logger:  w.logger,
	}
	w.internalWorker.RegisterHandler(topicName, internalHandler, lockDuration, variables)
	return w
}

// SetMaxTasks sets the maximum number of tasks to fetch per poll
// Returns the worker for method chaining
func (w *Worker) SetMaxTasks(maxTasks int) *Worker {
	w.internalWorker.SetMaxTasks(maxTasks)
	return w
}

// SetPollInterval sets the interval between polls when no tasks are available
// Returns the worker for method chaining
func (w *Worker) SetPollInterval(interval time.Duration) *Worker {
	w.internalWorker.SetPollInterval(interval)
	return w
}

// Start begins polling for external tasks
// This is a blocking call that will run until the context is cancelled
func (w *Worker) Start(ctx context.Context) {
	w.internalWorker.Start(ctx)
}

// handlerAdapter adapts the public TaskHandler interface to the internal interface
type handlerAdapter struct {
	handler TaskHandler
	client  *Client
	logger  *slog.Logger
}

func (ha *handlerAdapter) Handle(ctx context.Context, task worker.ExternalTask, complete worker.CompleteFunc, fail worker.FailFunc) error {
	ha.logger.Info("Processing task", "taskID", task.ID, "topic", task.TopicName)

	err := ha.handler.Handle(ctx, ha.client, task)
	if err != nil {
		ha.logger.Error("Task processing failed", "taskID", task.ID, "topic", task.TopicName, "error", err)
		// Report failure to Camunda
		failErr := fail("Task processing failed", err.Error(), 3, 30000)
		if failErr != nil {
			ha.logger.Error("Failed to report task failure", "taskID", task.ID, "error", failErr)
		}
		return err
	}

	ha.logger.Info("Task processed successfully", "taskID", task.ID, "topic", task.TopicName)
	return nil
}
