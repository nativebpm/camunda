package camunda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nativebpm/httpstream"
)

// ErrTaskDelegated is a sentinel error that indicates a task was successfully
// delegated for asynchronous execution and should not be completed by the worker.
var ErrTaskDelegated = errors.New("camunda: task delegated for asynchronous execution")

// Client represents a Camunda external task client
type Client struct {
	httpClient *httpstream.Client
	workerID   string
}

// NewClient creates a new Camunda external task client
func NewClient(hostURL, workerID string) (*Client, error) {
	baseURL := hostURL + "/engine-rest"
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}
	httpClient, err := httpstream.NewClient(&http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}, baseURL)
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

// Complete creates a new TaskCompletion builder
func (c *Client) Complete(taskID string) *TaskCompletion {
	return NewTaskCompletion(c.httpClient, c.workerID, taskID)
}

// Failure creates a new TaskFailure builder
func (c *Client) Failure(taskID string) *TaskFailure {
	return NewTaskFailure(c.httpClient, c.workerID, taskID)
}

// BpmnError creates a new TaskBpmnError builder
func (c *Client) BpmnError(taskID, errorCode, errorMessage string) *TaskBpmnError {
	return NewTaskBpmnError(c.httpClient, c.workerID, taskID, errorCode, errorMessage)
}

// ExtendLock creates a new LockExtension builder
func (c *Client) ExtendLock(taskID string, newDuration int) *LockExtension {
	return NewLockExtension(c.httpClient, c.workerID, taskID, newDuration)
}

// Unlock creates a new TaskUnlock builder
func (c *Client) Unlock(taskID string) *TaskUnlock {
	return NewTaskUnlock(c.httpClient, c.workerID, taskID)
}

// Lock creates a new TaskLock builder
func (c *Client) Lock(taskID string, duration int) *TaskLock {
	return NewTaskLock(c.httpClient, c.workerID, taskID, duration)
}

// GetProcessVariables retrieves all variables of a process instance from Camunda REST API
func (c *Client) GetProcessVariables(ctx context.Context, processInstanceID string) (map[string]Variable, error) {
	resp, err := c.httpClient.GET(ctx, "/process-instance/{id}/variables").
		PathParam("id", processInstanceID).
		Send()
	if err != nil {
		return nil, fmt.Errorf("failed to send get variables request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get variables request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var variables map[string]Variable
	if err := json.Unmarshal(body, &variables); err != nil {
		return nil, fmt.Errorf("failed to unmarshal variables: %w", err)
	}

	return variables, nil
}

// GetExecutionVariables retrieves all variables visible to an execution (including local ones)
func (c *Client) GetExecutionVariables(ctx context.Context, executionID string) (map[string]Variable, error) {
	resp, err := c.httpClient.GET(ctx, "/execution/{id}/variables").
		PathParam("id", executionID).
		Send()
	if err != nil {
		return nil, fmt.Errorf("failed to send get execution variables request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get execution variables request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var variables map[string]Variable
	if err := json.Unmarshal(body, &variables); err != nil {
		return nil, fmt.Errorf("failed to unmarshal variables: %w", err)
	}

	return variables, nil
}

// GetProcessInstanceBusinessKey retrieves the business key of a process instance
func (c *Client) GetProcessInstanceBusinessKey(ctx context.Context, processInstanceID string) (string, error) {
	resp, err := c.httpClient.GET(ctx, "/process-instance/{id}").
		PathParam("id", processInstanceID).
		Send()
	if err != nil {
		return "", fmt.Errorf("failed to get process instance: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get process instance failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		BusinessKey string `json:"businessKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode process instance: %w", err)
	}

	return result.BusinessKey, nil
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
