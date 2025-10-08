package builder

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/nativebpm/connectors/httpclient"
)

// Variable represents a Camunda variable with type safety
type Variable struct {
	Value     any    `json:"value"`
	Type      string `json:"type"`
	ValueInfo any    `json:"valueInfo,omitempty"`
}

// TaskCompletion provides a fluent API for completing external tasks
type TaskCompletion struct {
	httpClient     *httpclient.HTTPClient
	workerID       string
	ctx            context.Context
	taskID         string
	variables      map[string]Variable
	localVariables map[string]Variable
}

// NewTaskCompletion creates a new TaskCompletion builder
func NewTaskCompletion(httpClient *httpclient.HTTPClient, workerID, taskID string) *TaskCompletion {
	return &TaskCompletion{
		httpClient:     httpClient,
		workerID:       workerID,
		ctx:            context.Background(),
		taskID:         taskID,
		variables:      make(map[string]Variable),
		localVariables: make(map[string]Variable),
	}
}

// Context sets the context for the completion request
func (tc *TaskCompletion) Context(ctx context.Context) *TaskCompletion {
	tc.ctx = ctx
	return tc
}

// Variable adds a process variable
func (tc *TaskCompletion) Variable(name string, value Variable) *TaskCompletion {
	tc.variables[name] = value
	return tc
}

// Variables adds multiple process variables
func (tc *TaskCompletion) Variables(vars map[string]Variable) *TaskCompletion {
	for k, v := range vars {
		tc.variables[k] = v
	}
	return tc
}

// LocalVariable adds a local variable
func (tc *TaskCompletion) LocalVariable(name string, value Variable) *TaskCompletion {
	tc.localVariables[name] = value
	return tc
}

// LocalVariables adds multiple local variables
func (tc *TaskCompletion) LocalVariables(vars map[string]Variable) *TaskCompletion {
	for k, v := range vars {
		tc.localVariables[k] = v
	}
	return tc
}

// Execute sends the completion request
func (tc *TaskCompletion) Execute() error {
	req := struct {
		WorkerID       string              `json:"workerId"`
		Variables      map[string]Variable `json:"variables,omitempty"`
		LocalVariables map[string]Variable `json:"localVariables,omitempty"`
	}{
		WorkerID:       tc.workerID,
		Variables:      tc.variables,
		LocalVariables: tc.localVariables,
	}

	resp, err := tc.httpClient.POST(tc.ctx, "/external-task/{taskID}/complete").
		PathParam("taskID", tc.taskID).
		JSON(req).
		Send()
	if err != nil {
		return fmt.Errorf("failed to send complete request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("complete request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// TaskFailure provides a fluent API for reporting task failures
type TaskFailure struct {
	httpClient   *httpclient.HTTPClient
	workerID     string
	ctx          context.Context
	taskID       string
	errorMessage string
	errorDetails string
	retries      int
	retryTimeout int
}

// NewTaskFailure creates a new TaskFailure builder
func NewTaskFailure(httpClient *httpclient.HTTPClient, workerID, taskID string) *TaskFailure {
	return &TaskFailure{
		httpClient:   httpClient,
		workerID:     workerID,
		ctx:          context.Background(),
		taskID:       taskID,
		retries:      0,
		retryTimeout: 0,
	}
}

// Context sets the context for the failure request
func (tf *TaskFailure) Context(ctx context.Context) *TaskFailure {
	tf.ctx = ctx
	return tf
}

// ErrorMessage sets the error message
func (tf *TaskFailure) ErrorMessage(msg string) *TaskFailure {
	tf.errorMessage = msg
	return tf
}

// ErrorDetails sets the error details
func (tf *TaskFailure) ErrorDetails(details string) *TaskFailure {
	tf.errorDetails = details
	return tf
}

// Retries sets the number of retries
func (tf *TaskFailure) Retries(count int) *TaskFailure {
	tf.retries = count
	return tf
}

// RetryTimeout sets the retry timeout in milliseconds
func (tf *TaskFailure) RetryTimeout(timeout int) *TaskFailure {
	tf.retryTimeout = timeout
	return tf
}

// Execute sends the failure request
func (tf *TaskFailure) Execute() error {
	req := struct {
		WorkerID     string `json:"workerId"`
		ErrorMessage string `json:"errorMessage,omitempty"`
		ErrorDetails string `json:"errorDetails,omitempty"`
		Retries      int    `json:"retries,omitempty"`
		RetryTimeout int    `json:"retryTimeout,omitempty"`
	}{
		WorkerID:     tf.workerID,
		ErrorMessage: tf.errorMessage,
		ErrorDetails: tf.errorDetails,
		Retries:      tf.retries,
		RetryTimeout: tf.retryTimeout,
	}

	resp, err := tf.httpClient.POST(tf.ctx, "/external-task/{taskID}/failure").
		PathParam("taskID", tf.taskID).
		JSON(req).
		Send()
	if err != nil {
		return fmt.Errorf("failed to send failure request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failure request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// LockExtension provides a fluent API for extending task locks
type LockExtension struct {
	httpClient  *httpclient.HTTPClient
	workerID    string
	ctx         context.Context
	taskID      string
	newDuration int
}

// NewLockExtension creates a new LockExtension builder
func NewLockExtension(httpClient *httpclient.HTTPClient, workerID, taskID string, newDuration int) *LockExtension {
	return &LockExtension{
		httpClient:  httpClient,
		workerID:    workerID,
		ctx:         context.Background(),
		taskID:      taskID,
		newDuration: newDuration,
	}
}

// Context sets the context for the lock extension request
func (le *LockExtension) Context(ctx context.Context) *LockExtension {
	le.ctx = ctx
	return le
}

// Execute sends the lock extension request
func (le *LockExtension) Execute() error {
	req := struct {
		WorkerID    string `json:"workerId"`
		NewDuration int    `json:"newDuration"`
	}{
		WorkerID:    le.workerID,
		NewDuration: le.newDuration,
	}

	resp, err := le.httpClient.POST(le.ctx, "/external-task/{taskID}/extendLock").
		PathParam("taskID", le.taskID).
		JSON(req).
		Send()
	if err != nil {
		return fmt.Errorf("failed to send extendLock request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("extendLock request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// TaskUnlock provides a fluent API for unlocking tasks
type TaskUnlock struct {
	httpClient *httpclient.HTTPClient
	workerID   string
	ctx        context.Context
	taskID     string
}

// NewTaskUnlock creates a new TaskUnlock builder
func NewTaskUnlock(httpClient *httpclient.HTTPClient, workerID, taskID string) *TaskUnlock {
	return &TaskUnlock{
		httpClient: httpClient,
		workerID:   workerID,
		ctx:        context.Background(),
		taskID:     taskID,
	}
}

// Context sets the context for the unlock request
func (tu *TaskUnlock) Context(ctx context.Context) *TaskUnlock {
	tu.ctx = ctx
	return tu
}

// Execute sends the unlock request
func (tu *TaskUnlock) Execute() error {
	req := struct {
		WorkerID string `json:"workerId"`
	}{
		WorkerID: tu.workerID,
	}

	resp, err := tu.httpClient.POST(tu.ctx, "/external-task/{taskID}/unlock").
		PathParam("taskID", tu.taskID).
		JSON(req).
		Send()
	if err != nil {
		return fmt.Errorf("failed to send unlock request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unlock request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
