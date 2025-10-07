package camunda

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/nativebpm/connectors/httpclient"
)

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

// Variable represents a Camunda variable with type safety
type Variable struct {
	Value     any    `json:"value"`
	Type      string `json:"type"`
	ValueInfo any    `json:"valueInfo,omitempty"`
}

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
func JSONVariable(value any) Variable {
	return Variable{
		Value: value,
		Type:  "Json",
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
func NewClient(baseURL, workerID string) (*Client, error) {
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

// FetchAndLock fetches and locks external tasks for the given topics
func (c *Client) FetchAndLock(ctx context.Context, topics []TopicRequest, maxTasks int, asyncResponseTimeout *int) ([]ExternalTask, error) {
	req := struct {
		WorkerID             string         `json:"workerId"`
		MaxTasks             int            `json:"maxTasks"`
		UsePriority          bool           `json:"usePriority"`
		Topics               []TopicRequest `json:"topics"`
		AsyncResponseTimeout *int           `json:"asyncResponseTimeout,omitempty"`
	}{
		WorkerID:             c.workerID,
		MaxTasks:             maxTasks,
		UsePriority:          true,
		Topics:               topics,
		AsyncResponseTimeout: asyncResponseTimeout,
	}

	resp, err := c.httpClient.POST(ctx, "/external-task/fetchAndLock").
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

// Complete completes an external task
func (c *Client) Complete(ctx context.Context, taskID string, variables map[string]Variable, localVariables map[string]Variable) error {
	req := struct {
		WorkerID       string              `json:"workerId"`
		Variables      map[string]Variable `json:"variables,omitempty"`
		LocalVariables map[string]Variable `json:"localVariables,omitempty"`
	}{
		WorkerID:       c.workerID,
		Variables:      variables,
		LocalVariables: localVariables,
	}

	resp, err := c.httpClient.POST(ctx, "/external-task/{taskID}/complete").
		PathParam("taskID", taskID).
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

// HandleFailure handles failure of an external task
func (c *Client) HandleFailure(ctx context.Context, taskID string, errorMessage string, errorDetails string, retries int, retryTimeout int) error {
	req := struct {
		WorkerID     string `json:"workerId"`
		ErrorMessage string `json:"errorMessage,omitempty"`
		ErrorDetails string `json:"errorDetails,omitempty"`
		Retries      int    `json:"retries,omitempty"`
		RetryTimeout int    `json:"retryTimeout,omitempty"`
	}{
		WorkerID:     c.workerID,
		ErrorMessage: errorMessage,
		ErrorDetails: errorDetails,
		Retries:      retries,
		RetryTimeout: retryTimeout,
	}

	resp, err := c.httpClient.POST(context.Background(), "/external-task/{taskID}/failure").
		PathParam("taskID", taskID).
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

// ExtendLock extends the lock of an external task
func (c *Client) ExtendLock(ctx context.Context, taskID string, newDuration int) error {
	req := struct {
		WorkerID    string `json:"workerId"`
		NewDuration int    `json:"newDuration"`
	}{
		WorkerID:    c.workerID,
		NewDuration: newDuration,
	}

	resp, err := c.httpClient.POST(ctx, "/external-task/{taskID}/extendLock").
		PathParam("taskID", taskID).
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

// Unlock unlocks an external task
func (c *Client) Unlock(ctx context.Context, taskID string) error {
	req := struct {
		WorkerID string `json:"workerId"`
	}{
		WorkerID: c.workerID,
	}

	resp, err := c.httpClient.POST(ctx, "/external-task/{taskID}/unlock").
		PathParam("taskID", taskID).
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
