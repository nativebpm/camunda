package camunda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nativebpm/connectors/httpclient"
)

func TestNewClient(t *testing.T) {
	baseURL := "http://localhost:8080/engine-rest"
	workerID := "test-worker"

	client, err := NewClient(baseURL, workerID)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	if client.workerID != workerID {
		t.Errorf("expected workerID %s, got %s", workerID, client.workerID)
	}

	if client.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

func TestStringVariable(t *testing.T) {
	value := "test"
	v := StringVariable(value)

	if v.Value != value {
		t.Errorf("expected value %s, got %v", value, v.Value)
	}

	if v.Type != "String" {
		t.Errorf("expected type String, got %s", v.Type)
	}
}

func TestIntVariable(t *testing.T) {
	value := int64(42)
	v := IntVariable(value)

	if v.Value != value {
		t.Errorf("expected value %d, got %v", value, v.Value)
	}

	if v.Type != "Integer" {
		t.Errorf("expected type Integer, got %s", v.Type)
	}
}

func TestLongVariable(t *testing.T) {
	value := int64(123456789)
	v := LongVariable(value)

	if v.Value != value {
		t.Errorf("expected value %d, got %v", value, v.Value)
	}

	if v.Type != "Long" {
		t.Errorf("expected type Long, got %s", v.Type)
	}
}

func TestDoubleVariable(t *testing.T) {
	value := 3.14
	v := DoubleVariable(value)

	if v.Value != value {
		t.Errorf("expected value %f, got %v", value, v.Value)
	}

	if v.Type != "Double" {
		t.Errorf("expected type Double, got %s", v.Type)
	}
}

func TestBooleanVariable(t *testing.T) {
	value := true
	v := BooleanVariable(value)

	if v.Value != value {
		t.Errorf("expected value %t, got %v", value, v.Value)
	}

	if v.Type != "Boolean" {
		t.Errorf("expected type Boolean, got %s", v.Type)
	}
}

func TestDateVariable(t *testing.T) {
	value := time.Date(2023, 10, 1, 12, 0, 0, 0, time.UTC)
	v := DateVariable(value)

	expected := value.Format(time.RFC3339)
	if v.Value != expected {
		t.Errorf("expected value %s, got %v", expected, v.Value)
	}

	if v.Type != "Date" {
		t.Errorf("expected type Date, got %s", v.Type)
	}
}

func TestJSONVariable(t *testing.T) {
	value := map[string]string{"key": "value"}
	v := JSONVariable(value)

	if v.Value == nil {
		t.Error("expected value not nil")
	}

	if v.Type != "Object" {
		t.Errorf("expected type Object, got %s", v.Type)
	}

	// Verify valueInfo is present
	if v.ValueInfo == nil {
		t.Error("expected valueInfo not nil for JSON Object type")
	}

	// Verify value is a JSON string
	str, ok := v.Value.(string)
	if !ok {
		t.Errorf("expected value to be string, got %T", v.Value)
	}

	// Verify it's valid JSON
	expected := `{"key":"value"}`
	if str != expected {
		t.Errorf("expected JSON %s, got %s", expected, str)
	}
}

func TestJSONVariable_Array(t *testing.T) {
	value := []int{1, 2, 3}
	v := JSONVariable(value)

	if v.Type != "Object" {
		t.Errorf("expected type Object, got %s", v.Type)
	}

	// Verify valueInfo is present
	if v.ValueInfo == nil {
		t.Error("expected valueInfo not nil for JSON Object type")
	}

	// Verify value is a JSON string
	str, ok := v.Value.(string)
	if !ok {
		t.Errorf("expected value to be string, got %T", v.Value)
	}

	// Verify it's valid JSON array
	expected := `[1,2,3]`
	if str != expected {
		t.Errorf("expected JSON %s, got %s", expected, str)
	}
}

func TestNullVariable(t *testing.T) {
	v := NullVariable()

	if v.Value != nil {
		t.Errorf("expected value nil, got %v", v.Value)
	}

	if v.Type != "Null" {
		t.Errorf("expected type Null, got %s", v.Type)
	}
}

func TestComplete(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/external-task/task1/complete" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Check request body
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req["workerId"] != "test-worker" {
			t.Errorf("expected workerId test-worker, got %v", req["workerId"])
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Create client
	httpClient, _ := httpclient.NewClient(http.Client{}, server.URL)
	client := &Client{
		httpClient: httpClient,
		workerID:   "test-worker",
	}

	// Test Complete
	err := client.Complete("task1").Context(context.Background()).Execute()
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
}

func TestHandleFailure(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/external-task/task1/failure" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Check request body
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req["workerId"] != "test-worker" {
			t.Errorf("expected workerId test-worker, got %v", req["workerId"])
		}

		if req["errorMessage"] != "test error" {
			t.Errorf("expected errorMessage 'test error', got %v", req["errorMessage"])
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Create client
	httpClient, _ := httpclient.NewClient(http.Client{}, server.URL)
	client := &Client{
		httpClient: httpClient,
		workerID:   "test-worker",
	}

	// Test HandleFailure
	err := client.Failure("task1").
		Context(context.Background()).
		ErrorMessage("test error").
		ErrorDetails("details").
		Retries(3).
		RetryTimeout(1000).
		Execute()
	if err != nil {
		t.Fatalf("HandleFailure failed: %v", err)
	}
}

func TestExtendLock(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/external-task/task1/extendLock" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Check request body
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req["workerId"] != "test-worker" {
			t.Errorf("expected workerId test-worker, got %v", req["workerId"])
		}

		if req["newDuration"] != float64(60000) {
			t.Errorf("expected newDuration 60000, got %v", req["newDuration"])
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Create client
	httpClient, _ := httpclient.NewClient(http.Client{}, server.URL)
	client := &Client{
		httpClient: httpClient,
		workerID:   "test-worker",
	}

	// Test ExtendLock
	err := client.ExtendLock("task1", 60000).Context(context.Background()).Execute()
	if err != nil {
		t.Fatalf("ExtendLock failed: %v", err)
	}
}

func TestUnlock(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/external-task/task1/unlock" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Check request body
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req["workerId"] != "test-worker" {
			t.Errorf("expected workerId test-worker, got %v", req["workerId"])
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Create client
	httpClient, _ := httpclient.NewClient(http.Client{}, server.URL)
	client := &Client{
		httpClient: httpClient,
		workerID:   "test-worker",
	}

	// Test Unlock
	err := client.Unlock("task1").Context(context.Background()).Execute()
	if err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
}

func BenchmarkStringVariable(b *testing.B) {
	value := "test string"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = StringVariable(value)
	}
}

func BenchmarkIntVariable(b *testing.B) {
	value := int64(12345)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IntVariable(value)
	}
}

func BenchmarkDoubleVariable(b *testing.B) {
	value := 3.14159
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DoubleVariable(value)
	}
}

func BenchmarkBooleanVariable(b *testing.B) {
	value := true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BooleanVariable(value)
	}
}

func BenchmarkDateVariable(b *testing.B) {
	value := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DateVariable(value)
	}
}

func BenchmarkJSONVariable(b *testing.B) {
	value := map[string]any{"key": "value", "number": 42}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = JSONVariable(value)
	}
}

func BenchmarkNullVariable(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NullVariable()
	}
}

func BenchmarkNewClient(b *testing.B) {
	baseURL := "http://localhost:8080/engine-rest"
	workerID := "test-worker"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = NewClient(baseURL, workerID)
	}
}

func BenchmarkFetchAndLockRequestMarshal(b *testing.B) {
	topics := []TopicRequest{
		{
			TopicName:    "test-topic",
			LockDuration: 30000,
			Variables:    []string{"var1", "var2"},
		},
	}
	req := struct {
		WorkerID             string         `json:"workerId"`
		MaxTasks             int            `json:"maxTasks"`
		UsePriority          bool           `json:"usePriority"`
		Topics               []TopicRequest `json:"topics"`
		AsyncResponseTimeout *int           `json:"asyncResponseTimeout,omitempty"`
	}{
		WorkerID:    "test-worker",
		MaxTasks:    10,
		UsePriority: true,
		Topics:      topics,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(req)
	}
}

func BenchmarkCompleteRequestMarshal(b *testing.B) {
	variables := map[string]Variable{
		"var1": StringVariable("value1"),
		"var2": IntVariable(42),
	}
	req := struct {
		WorkerID       string              `json:"workerId"`
		Variables      map[string]Variable `json:"variables,omitempty"`
		LocalVariables map[string]Variable `json:"localVariables,omitempty"`
	}{
		WorkerID:  "test-worker",
		Variables: variables,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(req)
	}
}

func BenchmarkExternalTaskUnmarshal(b *testing.B) {
	data := `[{
		"id": "task1",
		"topicName": "test-topic",
		"workerId": "test-worker",
		"variables": {
			"var1": {"value": "value1", "type": "String"},
			"var2": {"value": 42, "type": "Integer"}
		}
	}]`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var tasks []ExternalTask
		_ = json.Unmarshal([]byte(data), &tasks)
	}
}
