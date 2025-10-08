package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/nativebpm/camunda/internal/builder"
	"github.com/nativebpm/connectors/httpclient"
)

// TestExternalTask_UnmarshalJSON tests parsing of Camunda timestamp formats
func TestExternalTask_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name: "Camunda format with milliseconds and +0000",
			json: `{
				"id": "task-1",
				"topicName": "test",
				"workerId": "worker-1",
				"lockExpirationTime": "2025-10-08T03:50:45.087+0000"
			}`,
			wantErr: false,
		},
		{
			name: "Camunda format without milliseconds",
			json: `{
				"id": "task-2",
				"topicName": "test",
				"workerId": "worker-1",
				"lockExpirationTime": "2025-10-08T03:50:45+0000"
			}`,
			wantErr: false,
		},
		{
			name: "RFC3339 format",
			json: `{
				"id": "task-3",
				"topicName": "test",
				"workerId": "worker-1",
				"lockExpirationTime": "2025-10-08T03:50:45Z"
			}`,
			wantErr: false,
		},
		{
			name: "RFC3339Nano format",
			json: `{
				"id": "task-4",
				"topicName": "test",
				"workerId": "worker-1",
				"lockExpirationTime": "2025-10-08T03:50:45.123456789Z"
			}`,
			wantErr: false,
		},
		{
			name: "No lockExpirationTime",
			json: `{
				"id": "task-5",
				"topicName": "test",
				"workerId": "worker-1"
			}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var task ExternalTask
			err := json.Unmarshal([]byte(tt.json), &task)

			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil {
				// Verify basic fields are parsed
				if task.ID == "" {
					t.Error("Expected task ID to be set")
				}
				if task.TopicName == "" {
					t.Error("Expected topic name to be set")
				}
			}
		})
	}
}

// MockHandler for testing
type MockHandler struct {
	called       bool
	calledWithID string
	err          error
	completeFn   CompleteFunc
	failFn       FailFunc
}

func (m *MockHandler) Handle(ctx context.Context, task ExternalTask, complete CompleteFunc, fail FailFunc) error {
	m.called = true
	m.calledWithID = task.ID
	m.completeFn = complete
	m.failFn = fail
	return m.err
}

func TestNew(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	worker := New(httpClient, "test-worker", logger)

	if worker == nil {
		t.Fatal("Expected worker to be created")
	}

	if worker.workerID != "test-worker" {
		t.Errorf("Expected workerID 'test-worker', got '%s'", worker.workerID)
	}

	if worker.httpClient != httpClient {
		t.Error("Expected httpClient to match provided client")
	}

	if worker.logger != logger {
		t.Error("Expected logger to match provided logger")
	}

	if worker.maxTasks != 10 {
		t.Errorf("Expected default maxTasks to be 10, got %d", worker.maxTasks)
	}

	if worker.pollInterval != 5*time.Second {
		t.Errorf("Expected default pollInterval to be 5s, got %v", worker.pollInterval)
	}

	if worker.handlers == nil {
		t.Error("Expected handlers map to be initialized")
	}

	if worker.topics == nil {
		t.Error("Expected topics slice to be initialized")
	}
}

func TestNew_NilLogger(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")

	worker := New(httpClient, "test-worker", nil)

	if worker == nil {
		t.Fatal("Expected worker to be created")
	}

	if worker.logger == nil {
		t.Error("Expected worker to have a default logger when nil is provided")
	}
}

func TestWorker_RegisterHandler(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")
	logger := slog.Default()
	worker := New(httpClient, "test-worker", logger)

	handler := &MockHandler{}
	result := worker.RegisterHandler("testTopic", handler, 60000, []string{"var1"})

	// Check fluent API
	if result != worker {
		t.Error("Expected RegisterHandler to return the worker for chaining")
	}

	// Check handler registration
	if len(worker.handlers) != 1 {
		t.Errorf("Expected 1 handler, got %d", len(worker.handlers))
	}

	if worker.handlers["testTopic"] != handler {
		t.Error("Expected handler to be registered for testTopic")
	}

	// Check topic registration
	if len(worker.topics) != 1 {
		t.Errorf("Expected 1 topic, got %d", len(worker.topics))
	}

	topic := worker.topics[0]
	if topic.TopicName != "testTopic" {
		t.Errorf("Expected topic name 'testTopic', got '%s'", topic.TopicName)
	}

	if topic.LockDuration != 60000 {
		t.Errorf("Expected lock duration 60000, got %d", topic.LockDuration)
	}

	if len(topic.Variables) != 1 || topic.Variables[0] != "var1" {
		t.Errorf("Expected variables ['var1'], got %v", topic.Variables)
	}
}

func TestWorker_RegisterHandler_Multiple(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")
	worker := New(httpClient, "test-worker", nil)

	handler1 := &MockHandler{}
	handler2 := &MockHandler{}

	worker.RegisterHandler("topic1", handler1, 60000, []string{"var1"})
	worker.RegisterHandler("topic2", handler2, 30000, []string{"var2", "var3"})

	if len(worker.handlers) != 2 {
		t.Errorf("Expected 2 handlers, got %d", len(worker.handlers))
	}

	if len(worker.topics) != 2 {
		t.Errorf("Expected 2 topics, got %d", len(worker.topics))
	}

	if worker.handlers["topic1"] != handler1 {
		t.Error("Expected handler1 to be registered for topic1")
	}

	if worker.handlers["topic2"] != handler2 {
		t.Error("Expected handler2 to be registered for topic2")
	}
}

func TestWorker_SetMaxTasks(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")
	worker := New(httpClient, "test-worker", nil)

	result := worker.SetMaxTasks(20)

	// Check fluent API
	if result != worker {
		t.Error("Expected SetMaxTasks to return the worker for chaining")
	}

	if worker.maxTasks != 20 {
		t.Errorf("Expected maxTasks to be 20, got %d", worker.maxTasks)
	}
}

func TestWorker_SetPollInterval(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")
	worker := New(httpClient, "test-worker", nil)

	interval := 10 * time.Second
	result := worker.SetPollInterval(interval)

	// Check fluent API
	if result != worker {
		t.Error("Expected SetPollInterval to return the worker for chaining")
	}

	if worker.pollInterval != interval {
		t.Errorf("Expected pollInterval to be %v, got %v", interval, worker.pollInterval)
	}
}

func TestWorker_FluentAPI(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")
	handler := &MockHandler{}

	// Test method chaining
	worker := New(httpClient, "test-worker", nil).
		RegisterHandler("topic1", handler, 60000, []string{"var1"}).
		RegisterHandler("topic2", handler, 60000, []string{"var2"}).
		SetMaxTasks(15).
		SetPollInterval(3 * time.Second)

	if worker == nil {
		t.Fatal("Expected worker to be created")
	}

	if len(worker.handlers) != 2 {
		t.Errorf("Expected 2 handlers, got %d", len(worker.handlers))
	}

	if worker.maxTasks != 15 {
		t.Errorf("Expected maxTasks to be 15, got %d", worker.maxTasks)
	}

	if worker.pollInterval != 3*time.Second {
		t.Errorf("Expected pollInterval to be 3s, got %v", worker.pollInterval)
	}
}

func TestWorker_ProcessTask(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock complete endpoint
		if r.URL.Path == "/external-task/task-123/complete" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	httpClient, _ := httpclient.NewClient(http.Client{}, server.URL)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := New(httpClient, "test-worker", logger)

	handler := &MockHandler{}
	worker.RegisterHandler("testTopic", handler, 60000, []string{})

	task := ExternalTask{
		ID:        "task-123",
		TopicName: "testTopic",
		WorkerID:  "test-worker",
		Variables: make(map[string]builder.Variable),
	}

	// Process task
	worker.processTask(context.Background(), task)

	// Verify handler was called
	if !handler.called {
		t.Error("Expected handler to be called")
	}

	if handler.calledWithID != "task-123" {
		t.Errorf("Expected handler to be called with task ID 'task-123', got '%s'", handler.calledWithID)
	}

	// Verify complete and fail functions were provided
	if handler.completeFn == nil {
		t.Error("Expected complete function to be provided to handler")
	}

	if handler.failFn == nil {
		t.Error("Expected fail function to be provided to handler")
	}
}

func TestWorker_ProcessTask_NoHandler(t *testing.T) {
	httpClient, _ := httpclient.NewClient(http.Client{}, "http://localhost:8080")
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := New(httpClient, "test-worker", logger)

	task := ExternalTask{
		ID:        "task-123",
		TopicName: "unknownTopic",
		WorkerID:  "test-worker",
		Variables: make(map[string]builder.Variable),
	}

	// Process task - should not panic
	worker.processTask(context.Background(), task)

	// No assertions needed - just verify it doesn't panic
}

func TestTaskHandler_Interface(t *testing.T) {
	// Compile-time check that MockHandler implements TaskHandler
	var _ TaskHandler = (*MockHandler)(nil)
}

func TestCompleteFunc(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/external-task/task-123/complete" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	httpClient, _ := httpclient.NewClient(http.Client{}, server.URL)
	worker := New(httpClient, "test-worker", nil)

	handler := &MockHandler{}
	worker.RegisterHandler("testTopic", handler, 60000, []string{})

	task := ExternalTask{
		ID:        "task-123",
		TopicName: "testTopic",
		Variables: make(map[string]builder.Variable),
	}

	worker.processTask(context.Background(), task)

	// Test the complete function that was provided to the handler
	if handler.completeFn != nil {
		vars := map[string]builder.Variable{
			"result": {Value: "success", Type: "String"},
		}
		err := handler.completeFn(vars)
		if err != nil {
			t.Errorf("Expected complete to succeed, got error: %v", err)
		}
	}
}

func TestFailFunc(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/external-task/task-123/failure" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	httpClient, _ := httpclient.NewClient(http.Client{}, server.URL)
	worker := New(httpClient, "test-worker", nil)

	handler := &MockHandler{}
	worker.RegisterHandler("testTopic", handler, 60000, []string{})

	task := ExternalTask{
		ID:        "task-123",
		TopicName: "testTopic",
		Variables: make(map[string]builder.Variable),
	}

	worker.processTask(context.Background(), task)

	// Test the fail function that was provided to the handler
	if handler.failFn != nil {
		err := handler.failFn("Task failed", "Detailed error", 3, 30000)
		if err != nil {
			t.Errorf("Expected fail to succeed, got error: %v", err)
		}
	}
}
