package tasks

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/nativebpm/camunda/internal/vars"
	"github.com/nativebpm/httpstream"
)

// rnd is a package-local random number generator used for jitter.
// Using a dedicated generator avoids mutating the global rand state
// and improves test isolation.
var rnd = rand.New(rand.NewSource(time.Now().UnixNano()))

// TaskCompletion provides a fluent API for completing external tasks
type TaskCompletion struct {
	httpClient     *httpstream.Client
	workerID       string
	ctx            context.Context
	taskID         string
	variables      map[string]vars.Variable
	localVariables map[string]vars.Variable
	logger         *slog.Logger
}

// NewTaskCompletion creates a new TaskCompletion builder
func NewTaskCompletion(httpClient *httpstream.Client, workerID, taskID string) *TaskCompletion {
	return &TaskCompletion{
		httpClient:     httpClient,
		workerID:       workerID,
		ctx:            context.Background(),
		taskID:         taskID,
		variables:      make(map[string]vars.Variable),
		localVariables: make(map[string]vars.Variable),
		logger:         slog.Default(),
	}
}

// Logger sets the logger for TaskCompletion
func (tc *TaskCompletion) Logger(logger *slog.Logger) *TaskCompletion {
	if logger != nil {
		tc.logger = logger
	}
	return tc
}

func (tc *TaskCompletion) Context(ctx context.Context) *TaskCompletion {
	tc.ctx = ctx
	return tc
}

// Typed fluent helpers for adding variables to the completion request.
// These mirror the helpers in the variables package and make it ergonomic
// to build up variables in handlers via the complete() factory.
func (tc *TaskCompletion) StringVariable(name, value string) *TaskCompletion {
	tc.variables[name] = vars.StringVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalStringVariable(name, value string) *TaskCompletion {
	tc.localVariables[name] = vars.StringVariable(value)
	return tc
}

func (tc *TaskCompletion) IntVariable(name string, value int) *TaskCompletion {
	tc.variables[name] = vars.IntVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalIntVariable(name string, value int) *TaskCompletion {
	tc.localVariables[name] = vars.IntVariable(value)
	return tc
}

func (tc *TaskCompletion) LongVariable(name string, value int) *TaskCompletion {
	tc.variables[name] = vars.LongVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalLongVariable(name string, value int) *TaskCompletion {
	tc.localVariables[name] = vars.LongVariable(value)
	return tc
}

func (tc *TaskCompletion) DoubleVariable(name string, value float64) *TaskCompletion {
	tc.variables[name] = vars.DoubleVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalDoubleVariable(name string, value float64) *TaskCompletion {
	tc.localVariables[name] = vars.DoubleVariable(value)
	return tc
}

func (tc *TaskCompletion) BooleanVariable(name string, value bool) *TaskCompletion {
	tc.variables[name] = vars.BooleanVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalBooleanVariable(name string, value bool) *TaskCompletion {
	tc.localVariables[name] = vars.BooleanVariable(value)
	return tc
}

func (tc *TaskCompletion) DateVariable(name string, value time.Time) *TaskCompletion {
	tc.variables[name] = vars.DateVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalDateVariable(name string, value time.Time) *TaskCompletion {
	tc.localVariables[name] = vars.DateVariable(value)
	return tc
}

func (tc *TaskCompletion) JSONVariable(name string, value any) *TaskCompletion {
	tc.variables[name] = vars.JSONVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalJSONVariable(name string, value any) *TaskCompletion {
	tc.localVariables[name] = vars.JSONVariable(value)
	return tc
}

func (tc *TaskCompletion) ListVariable(name string, value any) *TaskCompletion {
	tc.variables[name] = vars.ListVariable(value)
	return tc
}

func (tc *TaskCompletion) LocalListVariable(name string, value any) *TaskCompletion {
	tc.localVariables[name] = vars.ListVariable(value)
	return tc
}

func (tc *TaskCompletion) NullVariable(name string) *TaskCompletion {
	tc.variables[name] = vars.NullVariable()
	return tc
}

func (tc *TaskCompletion) LocalNullVariable(name string) *TaskCompletion {
	tc.localVariables[name] = vars.NullVariable()
	return tc
}

func (tc *TaskCompletion) Execute() error {
	req := struct {
		WorkerID       string                   `json:"workerId"`
		Variables      map[string]vars.Variable `json:"variables,omitempty"`
		LocalVariables map[string]vars.Variable `json:"localVariables,omitempty"`
	}{
		WorkerID:       tc.workerID,
		Variables:      tc.variables,
		LocalVariables: tc.localVariables,
	}

	// Retry on optimistic locking exceptions returned by Camunda
	const maxRetries = 3
	baseBackoff := 100 * time.Millisecond

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := tc.httpClient.POST(tc.ctx, "/external-task/{taskID}/complete").
			PathParam("taskID", tc.taskID).
			JSON(req).
			Send()
		if err != nil {
			return fmt.Errorf("failed to send complete request: %w", err)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("failed to read response body: %w", readErr)
		}

		if resp.StatusCode == http.StatusNoContent {
			return nil
		}

		// Log diagnostic details for non-204 responses so engine errors
		// (e.g., OptimisticLockingException) are visible in application output.
		tc.logger.Debug("complete failed",
			"taskID", tc.taskID,
			"attempt", attempt,
			"status", resp.StatusCode,
			"response", string(body),
			"variables", req.Variables,
		)

		// If we get an optimistic locking exception, retry with backoff
		if resp.StatusCode == http.StatusInternalServerError && strings.Contains(string(body), "OptimisticLockingException") {
			if attempt < maxRetries {
				// exponential backoff with small jitter (context-aware)
				sleep := baseBackoff * (1 << attempt)
				sleep += time.Duration(rnd.Intn(100)) * time.Millisecond

				tc.logger.Info("optimistic locking, retrying",
					"taskID", tc.taskID,
					"attempt", attempt,
					"backoff", sleep,
				)

				timer := time.NewTimer(sleep)
				select {
				case <-tc.ctx.Done():
					timer.Stop()
					return tc.ctx.Err()
				case <-timer.C:
					// continue to next attempt
				}
				continue
			}
			tc.logger.Warn("complete failed after retries due to optimistic locking",
				"taskID", tc.taskID,
				"retries", maxRetries,
			)
			return fmt.Errorf("complete request failed after %d retries due to optimistic locking: %s", maxRetries, string(body))
		}

		return fmt.Errorf("complete request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return fmt.Errorf("complete request failed after retries")
}

// TaskFailure preovides a fluent API for reporting task failures
type TaskFailure struct {
	httpClient   *httpstream.Client
	workerID     string
	ctx          context.Context
	taskID       string
	errorMessage string
	errorDetails string
	retries      int
	retryTimeout int
}

// NewTaskFailure creates a new TaskFailure builder
func NewTaskFailure(httpClient *httpstream.Client, workerID, taskID string) *TaskFailure {
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
	httpClient  *httpstream.Client
	workerID    string
	ctx         context.Context
	taskID      string
	newDuration int
}

// NewLockExtension creates a new LockExtension builder
func NewLockExtension(httpClient *httpstream.Client, workerID, taskID string, newDuration int) *LockExtension {
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
	httpClient *httpstream.Client
	workerID   string
	ctx        context.Context
	taskID     string
}

// NewTaskUnlock creates a new TaskUnlock builder
func NewTaskUnlock(httpClient *httpstream.Client, workerID, taskID string) *TaskUnlock {
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

// TaskLock provides a fluent API for locking tasks
type TaskLock struct {
	httpClient *httpstream.Client
	workerID   string
	ctx        context.Context
	taskID     string
	duration   int
}

// NewTaskLock creates a new TaskLock builder
func NewTaskLock(httpClient *httpstream.Client, workerID, taskID string, duration int) *TaskLock {
	return &TaskLock{
		httpClient: httpClient,
		workerID:   workerID,
		ctx:        context.Background(),
		taskID:     taskID,
		duration:   duration,
	}
}

// Context sets the context for the lock request
func (tl *TaskLock) Context(ctx context.Context) *TaskLock {
	tl.ctx = ctx
	return tl
}

// Execute sends the lock request
func (tl *TaskLock) Execute() error {
	req := struct {
		WorkerID     string `json:"workerId"`
		LockDuration int    `json:"lockDuration"`
	}{
		WorkerID:     tl.workerID,
		LockDuration: tl.duration,
	}

	resp, err := tl.httpClient.POST(tl.ctx, "/external-task/{taskID}/lock").
		PathParam("taskID", tl.taskID).
		JSON(req).
		Send()
	if err != nil {
		return fmt.Errorf("failed to send lock request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("lock request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// TaskBpmnError provides a fluent API for reporting BPMN errors
type TaskBpmnError struct {
	httpClient   *httpstream.Client
	workerID     string
	ctx          context.Context
	taskID       string
	errorCode    string
	errorMessage string
	variables    map[string]vars.Variable
}

// NewTaskBpmnError creates a new TaskBpmnError builder
func NewTaskBpmnError(httpClient *httpstream.Client, workerID, taskID, errorCode, errorMessage string) *TaskBpmnError {
	return &TaskBpmnError{
		httpClient:   httpClient,
		workerID:     workerID,
		ctx:          context.Background(),
		taskID:       taskID,
		errorCode:    errorCode,
		errorMessage: errorMessage,
		variables:    make(map[string]vars.Variable),
	}
}

// Context sets the context for the BPMN error request
func (te *TaskBpmnError) Context(ctx context.Context) *TaskBpmnError {
	te.ctx = ctx
	return te
}

// StringVariable adds a string variable to the BPMN error request
func (te *TaskBpmnError) StringVariable(name, value string) *TaskBpmnError {
	te.variables[name] = vars.StringVariable(value)
	return te
}

// IntVariable adds an int variable to the BPMN error request
func (te *TaskBpmnError) IntVariable(name string, value int) *TaskBpmnError {
	te.variables[name] = vars.IntVariable(value)
	return te
}

// BooleanVariable adds a boolean variable to the BPMN error request
func (te *TaskBpmnError) BooleanVariable(name string, value bool) *TaskBpmnError {
	te.variables[name] = vars.BooleanVariable(value)
	return te
}

// Execute sends the BPMN error request
func (te *TaskBpmnError) Execute() error {
	req := struct {
		WorkerID     string                   `json:"workerId"`
		ErrorCode    string                   `json:"errorCode"`
		ErrorMessage string                   `json:"errorMessage,omitempty"`
		Variables    map[string]vars.Variable `json:"variables,omitempty"`
	}{
		WorkerID:     te.workerID,
		ErrorCode:    te.errorCode,
		ErrorMessage: te.errorMessage,
		Variables:    te.variables,
	}

	resp, err := te.httpClient.POST(te.ctx, "/external-task/{taskID}/bpmnError").
		PathParam("taskID", te.taskID).
		JSON(req).
		Send()
	if err != nil {
		return fmt.Errorf("failed to send bpmnError request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("bpmnError request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

