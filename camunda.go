package camunda

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nativebpm/camunda/internal/tasks"
	"github.com/nativebpm/camunda/internal/worker"
	"github.com/nativebpm/httpstream"
)

type ExternalTask = worker.ExternalTask
type CompleteFunc = worker.CompleteFunc
type FailFunc = worker.FailFunc
type TopicRequest = worker.TopicRequest

// Client represents a Camunda external task client
type Client struct {
	httpClient *httpstream.Client
	workerID   string
}

// NewClient creates a new Camunda external task client
func NewClient(hostURL, workerID string) (*Client, error) {
	baseURL := hostURL + "/engine-rest"
	httpClient, err := httpstream.NewClient(&http.Client{Timeout: 30 * time.Second}, baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return &Client{
		httpClient: httpClient,
		workerID:   workerID,
	}, nil
}

func (c *Client) Use(middleware func(http.RoundTripper) http.RoundTripper) *Client {
	c.httpClient = c.httpClient.Use(middleware)
	return c
}

// TaskCompletion provides a fluent API for completing external tasks
type TaskCompletion = tasks.TaskCompletion

// Complete creates a new TaskCompletion builder
func (c *Client) Complete(taskID string) *TaskCompletion {
	return tasks.NewTaskCompletion(c.httpClient, c.workerID, taskID)
}

// TaskFailure provides a fluent API for reporting task failures
type TaskFailure = tasks.TaskFailure

// Failure creates a new TaskFailure builder
func (c *Client) Failure(taskID string) *TaskFailure {
	return tasks.NewTaskFailure(c.httpClient, c.workerID, taskID)
}

// LockExtension provides a fluent API for extending task locks
type LockExtension = tasks.LockExtension

// ExtendLock creates a new LockExtension builder
func (c *Client) ExtendLock(taskID string, newDuration int) *LockExtension {
	return tasks.NewLockExtension(c.httpClient, c.workerID, taskID, newDuration)
}

// TaskUnlock provides a fluent API for unlocking tasks
type TaskUnlock = tasks.TaskUnlock

// Unlock creates a new TaskUnlock builder
func (c *Client) Unlock(taskID string) *TaskUnlock {
	return tasks.NewTaskUnlock(c.httpClient, c.workerID, taskID)
}

// StartProcessInstance starts a process instance and sets the businessKey
// The variables map can be nil or empty; heavy application data should be stored
// outside Camunda (for example in an in-memory store) and referenced by businessKey.
func (c *Client) StartProcessInstance(ctx context.Context, processDefinitionKey, businessKey string, variables map[string]Variable) (string, error) {
	// Prepare request payload
	payload := map[string]any{"businessKey": businessKey, "variables": variables}

	resp, err := c.httpClient.POST(ctx, "/process-definition/key/{processDefinitionKey}/start").
		PathParam("processDefinitionKey", processDefinitionKey).
		JSON(payload).
		Send()

	if err != nil {
		return "", fmt.Errorf("failed to send start process request: %w", err)
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("failed to read response body: %w", readErr)
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

// EvaluateDecision evaluates a DMN decision table by its key.
func (c *Client) EvaluateDecision(ctx context.Context, decisionDefinitionKey string, variables map[string]Variable) ([]map[string]Variable, error) {
	payload := map[string]any{"variables": variables}

	resp, err := c.httpClient.POST(ctx, "/decision-definition/key/{decisionDefinitionKey}/evaluate").
		PathParam("decisionDefinitionKey", decisionDefinitionKey).
		JSON(payload).
		Send()

	if err != nil {
		return nil, fmt.Errorf("failed to send evaluate decision request: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("evaluate decision request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result []map[string]Variable
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal decision result: %w", err)
	}

	return result, nil
}

// DeleteDeployment deletes a deployment by ID. If cascade is true, it also deletes all process instances,
// decision instances, and historic occurrences associated with this deployment.
func (c *Client) DeleteDeployment(ctx context.Context, deploymentID string, cascade bool) error {
	req := c.httpClient.DELETE(ctx, "/deployment/{id}").
		PathParam("id", deploymentID)

	if cascade {
		req = req.Param("cascade", "true")
	}

	resp, err := req.Send()
	if err != nil {
		return fmt.Errorf("failed to send delete deployment request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete deployment request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// TaskHandler defines the interface for external task handlers
// Handlers implement business logic for specific topics
type TaskHandler interface {
	// Handler receives a client and task and factories for completing or
	// failing the task. Handlers may either use the client convenience
	// methods (client.Complete) or use the provided factories for fluent
	// variable building:
	//   complete().StringVariable("ok", "yes").Execute()
	Handle(ctx context.Context, client *Client, task ExternalTask, complete CompleteFunc, fail FailFunc) error
}

// TaskHandlerFunc is an adapter to allow the use of ordinary functions as task handlers.
type TaskHandlerFunc func(ctx context.Context, client *Client, task ExternalTask, complete CompleteFunc, fail FailFunc) error

// Handle calls f(ctx, client, task, complete, fail).
func (f TaskHandlerFunc) Handle(ctx context.Context, client *Client, task ExternalTask, complete CompleteFunc, fail FailFunc) error {
	return f(ctx, client, task, complete, fail)
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

// SetMaxConcurrency sets the maximum number of tasks processed concurrently
// This limits resource usage and prevents overwhelming the system with too many parallel tasks
// Default is 20 (2x maxTasks)
// Returns the worker for method chaining
func (w *Worker) SetMaxConcurrency(maxConcurrency int) *Worker {
	w.internalWorker.SetMaxConcurrency(maxConcurrency)
	return w
}

// SetAsyncResponseTimeout enables long polling for fetchAndLock. Pass a time.Duration.
// Use 0 to disable async long polling.
func (w *Worker) SetAsyncResponseTimeout(timeout time.Duration) *Worker {
	w.internalWorker.SetAsyncResponseTimeout(timeout)
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

	// The public handler API remains `Handle(ctx, client, task) error`.
	// The internal worker provides a complete factory to the handler adapter,
	// but for backward compatibility we call the public handler and only
	// use the provided `complete` in case the handler wants to use the
	// fluent `Client.Complete` API on the client directly.

	err := ha.handler.Handle(ctx, ha.client, task, complete, fail)
	if err != nil {
		ha.logger.Error("Task processing failed", "taskID", task.ID, "topic", task.TopicName, "error", err)
		// Report failure to Camunda via provided fail func
		failErr := fail("Task processing failed", err.Error(), 3, 30000)
		if failErr != nil {
			ha.logger.Error("Failed to report task failure", "taskID", task.ID, "error", failErr)
		}
		return err
	}

	ha.logger.Info("Task processed successfully", "taskID", task.ID, "topic", task.TopicName)
	return nil
}
