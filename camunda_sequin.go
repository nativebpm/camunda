package camunda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nativebpm/camunda/internal/tasks"
)

// SequinWorker processes Camunda external tasks using logical replication logs from Sequin
// instead of polling via fetchAndLock. It performs task locking and variables fetching
// via official REST API, achieving clean architecture separation without direct DB access.
type SequinWorker struct {
	client     *Client
	sequinURL  string
	consumer   string
	apiToken   string
	logger     *slog.Logger
	handlers   map[string]TaskHandler
	workerID   string
	wg         sync.WaitGroup
	httpClient *http.Client
}

type sequinPayload struct {
	Action string         `json:"action"`
	Record map[string]any `json:"record"`
}

type sequinMessage struct {
	ID      string        `json:"id"`
	AckID   string        `json:"ack_id"`
	Payload sequinPayload `json:"data"`
}

type sequinReceiveResponse struct {
	Data []sequinMessage `json:"data"`
}

// NewSequinWorker creates a new Sequin logical replication worker
func NewSequinWorker(client *Client, sequinURL string, consumer string, logger *slog.Logger) (*SequinWorker, error) {
	if logger == nil {
		logger = slog.Default()
	}

	token := os.Getenv("SEQUIN_API_TOKEN")
	if token == "" {
		token = "sequin_loadtest_secret_token_12345"
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}

	return &SequinWorker{
		client:     client,
		sequinURL:  sequinURL,
		consumer:   consumer,
		apiToken:   token,
		logger:     logger,
		handlers:   make(map[string]TaskHandler),
		workerID:   client.workerID,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
	}, nil
}

// RegisterHandler registers a task handler for a topic
func (sw *SequinWorker) RegisterHandler(topicName string, handler TaskHandler) *SequinWorker {
	sw.handlers[topicName] = handler
	return sw
}

// Start begins processing logical replication changes from Sequin
func (sw *SequinWorker) Start(ctx context.Context) {
	sw.logger.Info("Starting Sequin logical replication worker",
		"sequin_url", sw.sequinURL,
		"consumer", sw.consumer,
		"worker_id", sw.workerID,
	)

	// Keep polling Sequin
	for {
		select {
		case <-ctx.Done():
			sw.logger.Info("Context cancelled, waiting for active tasks to complete...")
			sw.wg.Wait()
			return
		default:
			msgs, err := sw.receiveMessages(ctx)
			if err != nil {
				sw.logger.Error("Failed to receive messages from Sequin", "error", err)
				time.Sleep(1 * time.Second)
				continue
			}

			if len(msgs) == 0 {
				continue
			}

			for _, msg := range msgs {
				sw.wg.Add(1)
				go func(m sequinMessage) {
					defer sw.wg.Done()
					sw.processMessage(ctx, m)
				}(msg)
			}
		}
	}
}

func (sw *SequinWorker) receiveMessages(ctx context.Context) ([]sequinMessage, error) {
	url := fmt.Sprintf("%s/api/http_pull_consumers/%s/receive", sw.sequinURL, sw.consumer)
	reqBody, _ := json.Marshal(map[string]any{
		"batch_size": 20,
		"wait_for":   5000, // 5s long poll
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sw.apiToken)

	resp, err := sw.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sequin receive failed with status %d: %s", resp.StatusCode, string(body))
	}

	var res sequinReceiveResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}

	return res.Data, nil
}

func (sw *SequinWorker) ackMessage(ctx context.Context, ackID string) {
	url := fmt.Sprintf("%s/api/http_pull_consumers/%s/ack", sw.sequinURL, sw.consumer)
	reqBody, _ := json.Marshal(map[string]any{
		"ack_ids": []string{ackID},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		sw.logger.Error("Failed to build ack request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sw.apiToken)

	resp, err := sw.httpClient.Do(req)
	if err != nil {
		sw.logger.Error("Failed to send ack to Sequin", "error", err)
		return
	}
	resp.Body.Close()
}

func (sw *SequinWorker) nackMessage(ctx context.Context, ackID string) {
	url := fmt.Sprintf("%s/api/http_pull_consumers/%s/nack", sw.sequinURL, sw.consumer)
	reqBody, _ := json.Marshal(map[string]any{
		"ack_ids": []string{ackID},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		sw.logger.Error("Failed to build nack request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sw.apiToken)

	resp, err := sw.httpClient.Do(req)
	if err != nil {
		sw.logger.Error("Failed to send nack to Sequin", "error", err)
		return
	}
	resp.Body.Close()
}

func (sw *SequinWorker) processMessage(ctx context.Context, msg sequinMessage) {
	if msg.Payload.Action == "delete" {
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	sw.logger.Info("Received CDC message", "action", msg.Payload.Action, "record", msg.Payload.Record)

	taskID, _ := msg.Payload.Record["id_"].(string)
	topicName, _ := msg.Payload.Record["topic_name_"].(string)
	procInstID, _ := msg.Payload.Record["proc_inst_id_"].(string)
	executionID, _ := msg.Payload.Record["execution_id_"].(string)
	businessKey, _ := msg.Payload.Record["business_key_"].(string)

	if taskID == "" || topicName == "" {
		sw.logger.Warn("Ignored task due to missing ID or topic", "task_id", taskID, "topic", topicName)
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	handler, ok := sw.handlers[topicName]
	if !ok {
		// No handler registered for this topic, ack to remove from queue
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	// 1. Lock the task using Camunda REST API
	lockDurationMs := 30000 // 30 seconds
	lockExpiration := time.Now().Add(30 * time.Second)

	err := sw.client.Lock(taskID, lockDurationMs).Context(ctx).Execute()
	if err != nil {
		// If the lock failed (e.g. task was already completed or deleted in Camunda)
		// we should ack the message to discard it.
		sw.logger.Warn("Failed to acquire lock via REST API (task might be completed, deleted, or locked)", "task_id", taskID, "error", err)
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	sw.logger.Info("Logical CDC lock acquired on task via REST", "task_id", taskID, "topic", topicName)

	// 2. Query process variables via Camunda REST API
	variables, err := sw.client.GetProcessVariables(ctx, procInstID)
	if err != nil {
		sw.logger.Error("Failed to fetch process variables for task via REST", "task_id", taskID, "error", err)
		sw.nackMessage(context.Background(), msg.AckID)
		return
	}

	// 3. Construct ExternalTask
	task := ExternalTask{
		ID:                  taskID,
		TopicName:           topicName,
		WorkerID:            sw.workerID,
		ProcessInstanceID:   procInstID,
		ExecutionID:         executionID,
		BusinessKey:         businessKey,
		Variables:           variables,
		LockExpirationTime:  &lockExpiration,
	}

	// 4. Create complete and fail builders
	complete := func() *tasks.TaskCompletion {
		return tasks.NewTaskCompletion(sw.client.httpClient, sw.workerID, taskID).Context(ctx).Logger(sw.logger)
	}

	fail := func(errorMessage, errorDetails string, retries, retryTimeout int) error {
		return tasks.NewTaskFailure(sw.client.httpClient, sw.workerID, taskID).
			Context(ctx).
			ErrorMessage(errorMessage).
			ErrorDetails(errorDetails).
			Retries(retries).
			RetryTimeout(retryTimeout).
			Execute()
	}

	// 5. Execute handler
	if err := handler.Handle(ctx, sw.client, task, complete, fail); err != nil {
		sw.logger.Error("Task handler returned error", "task_id", taskID, "error", err)
		if strings.Contains(err.Error(), "OptimisticLockingException") {
			sw.logger.Warn("Optimistic locking collision during task execution, nacking in Sequin to retry", "task_id", taskID)
			sw.nackMessage(context.Background(), msg.AckID)
		} else {
			_ = fail(err.Error(), "Handler error", 0, 0)
			sw.ackMessage(context.Background(), msg.AckID)
		}
	} else {
		sw.logger.Info("Task processed successfully via Sequin", "task_id", taskID)
		sw.ackMessage(context.Background(), msg.AckID)
	}
}
