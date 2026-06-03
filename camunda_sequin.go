package camunda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sequinstream/sequin-go"
)

var rnd = rand.New(rand.NewSource(time.Now().UnixNano()))

// SequinWorker processes Camunda external tasks using logical replication logs from Sequin
// instead of polling via fetchAndLock. It performs task locking and variables fetching
// via official REST API, achieving clean architecture separation without direct DB access.
type SequinWorker struct {
	client         *Client
	sequinClient   *sequin.Client
	sequinURL      string
	token          string
	consumer       string
	logger         *slog.Logger
	handlers       map[string]TaskHandler
	lockDurations  map[string]int
	workerID       string
	wg             sync.WaitGroup
	maxConcurrency int
	taskSemaphore  chan struct{}
	httpClient     *http.Client
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

	opts := &sequin.ClientOptions{
		BaseURL: sequinURL,
	}
	sequinClient := sequin.NewClient(token, opts)

	maxConcurrency := 20
	httpClient := &http.Client{
		Timeout: 35 * time.Second,
	}

	return &SequinWorker{
		client:         client,
		sequinClient:   sequinClient,
		sequinURL:      sequinURL,
		token:          token,
		consumer:       consumer,
		logger:         logger,
		handlers:       make(map[string]TaskHandler),
		lockDurations:  make(map[string]int),
		workerID:       client.workerID,
		maxConcurrency: maxConcurrency,
		taskSemaphore:  make(chan struct{}, maxConcurrency),
		httpClient:     httpClient,
	}, nil
}

// RegisterHandler registers a task handler for a topic with default 30s lock duration
func (sw *SequinWorker) RegisterHandler(topicName string, handler TaskHandler) *SequinWorker {
	sw.handlers[topicName] = handler
	sw.lockDurations[topicName] = 30000 // default 30 seconds
	return sw
}

// RegisterHandlerWithOptions registers a task handler for a topic with custom lock duration
func (sw *SequinWorker) RegisterHandlerWithOptions(topicName string, handler TaskHandler, lockDurationMs int) *SequinWorker {
	sw.handlers[topicName] = handler
	sw.lockDurations[topicName] = lockDurationMs
	return sw
}

// SetMaxConcurrency sets the maximum number of concurrent task processors
func (sw *SequinWorker) SetMaxConcurrency(maxConcurrency int) *SequinWorker {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	sw.maxConcurrency = maxConcurrency
	sw.taskSemaphore = make(chan struct{}, maxConcurrency)
	return sw
}

// extTaskRecord represents the database schema of the act_ru_ext_task table captured by Sequin CDC.
// Using a struct instead of map[string]any dramatically reduces memory allocations.
type extTaskRecord struct {
	ID          string `json:"id_"`
	TopicName   string `json:"topic_name_"`
	ProcInstID  string `json:"proc_inst_id_"`
	ExecutionID string `json:"execution_id_"`
	BusinessKey string `json:"business_key_"`
}

type sequinMessagePayload struct {
	AckID string `json:"ack_id"`
	Data  struct {
		Record   json.RawMessage `json:"record"`
		Action   string          `json:"action"`
		Metadata struct {
			Enrichment struct {
				ID          string `json:"id_"`
				BusinessKey string `json:"business_key"`
				Variables   map[string]struct {
					Value any `json:"value"`
				} `json:"variables"`
			} `json:"enrichment"`
		} `json:"metadata"`
	} `json:"data"`
}

type receiveResponsePayload struct {
	Data []sequinMessagePayload `json:"data"`
}

// Start begins processing logical replication changes from Sequin
func (sw *SequinWorker) Start(ctx context.Context) {
	sw.logger.Info("Starting Sequin logical replication worker",
		"consumer", sw.consumer,
		"worker_id", sw.workerID,
		"max_concurrency", sw.maxConcurrency,
	)

	// Keep polling Sequin
	for {
		select {
		case <-ctx.Done():
			sw.logger.Info("Context canceled, waiting for active tasks to complete...")
			sw.wg.Wait()
			return
		default:
			// 1. Calculate free slots in our concurrency semaphore
			freeSlots := sw.maxConcurrency - len(sw.taskSemaphore)
			if freeSlots <= 0 {
				// Semaphore is fully saturated. Block until at least one worker slot is freed.
				select {
				case <-ctx.Done():
					sw.logger.Info("Context canceled, waiting for active tasks to complete...")
					sw.wg.Wait()
					return
				case sw.taskSemaphore <- struct{}{}:
					// Successfully acquired 1 slot, release it back immediately to allow normal flow
					<-sw.taskSemaphore
				}
			}

			// 2. Poll Sequin requesting a batch size matching max concurrency
			batchSize := sw.maxConcurrency
			if batchSize < 1 {
				batchSize = 20
			}
			if batchSize > 50 {
				batchSize = 50 // Keep HTTP response payload size reasonable
			}

			msgs, err := sw.receiveMessages(ctx, batchSize)
			if err != nil {
				sw.logger.Error("Failed to receive messages from Sequin", "error", err)
				time.Sleep(1 * time.Second)
				continue
			}

			if len(msgs) == 0 {
				continue
			}

			for _, msg := range msgs {
				// Acquire semaphore slot context-aware
				select {
				case <-ctx.Done():
					sw.logger.Info("Context canceled, waiting for active tasks to complete...")
					sw.wg.Wait()
					return
				case sw.taskSemaphore <- struct{}{}:
				}

				sw.wg.Add(1)
				go func(m sequinMessagePayload) {
					defer func() {
						<-sw.taskSemaphore
						sw.wg.Done()
					}()
					sw.processMessage(ctx, m)
				}(msg)
			}
		}
	}
}

func (sw *SequinWorker) receiveMessages(ctx context.Context, batchSize int) ([]sequinMessagePayload, error) {
	url := fmt.Sprintf("%s/api/http_pull_consumers/%s/receive", sw.sequinURL, sw.consumer)

	params := struct {
		BatchSize int `json:"batch_size"`
		WaitFor   int `json:"wait_for"`
	}{
		BatchSize: batchSize,
		WaitFor:   5000, // 5s long poll
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshaling receive params: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+sw.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := sw.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bad status code %d: %s", resp.StatusCode, string(respBody))
	}

	var res receiveResponsePayload
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return res.Data, nil
}

func (sw *SequinWorker) ackMessage(ctx context.Context, ackID string) {
	err := sw.sequinClient.Ack(ctx, sw.consumer, []string{ackID})
	if err != nil {
		sw.logger.Error("Failed to send ack to Sequin", "error", err)
	}
}

func (sw *SequinWorker) nackMessage(ctx context.Context, ackID string) {
	err := sw.sequinClient.Nack(ctx, sw.consumer, []string{ackID})
	if err != nil {
		sw.logger.Error("Failed to send nack to Sequin", "error", err)
	}
}

func (sw *SequinWorker) processMessage(ctx context.Context, msg sequinMessagePayload) {
	var record extTaskRecord
	if err := json.Unmarshal(msg.Data.Record, &record); err != nil {
		sw.logger.Error("Failed to unmarshal message record", "error", err)
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	sw.logger.Debug("Received CDC message", "task_id", record.ID, "topic", record.TopicName)

	if record.ID == "" || record.TopicName == "" {
		sw.logger.Warn("Ignored task due to missing ID or topic", "task_id", record.ID, "topic", record.TopicName)
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	handler, ok := sw.handlers[record.TopicName]
	if !ok {
		// No handler registered for this topic, ack to remove from queue
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	var variables map[string]Variable
	businessKey := record.BusinessKey
	var lockExpiration *time.Time

	// 1. Lock the task using Camunda REST API (Required for both modes to ensure exactly-once execution and owner check)
	lockDurationMs := 30000 // 30 seconds by default
	if customDuration, ok := sw.lockDurations[record.TopicName]; ok && customDuration > 0 {
		lockDurationMs = customDuration
	}
	exp := time.Now().Add(time.Duration(lockDurationMs) * time.Millisecond)
	lockExpiration = &exp

	err := sw.client.Lock(record.ID, lockDurationMs).Context(ctx).Execute()
	if err != nil {
		if strings.Contains(err.Error(), "status 404") {
			sw.logger.Debug("Task not found via REST API (likely completed or deleted), acking change", "task_id", record.ID)
			sw.ackMessage(context.Background(), msg.AckID)
			return
		}

		sw.logger.Warn("Lock request failed with transient error, nacking in Sequin", "task_id", record.ID, "error", err)
		sw.nackMessage(context.Background(), msg.AckID)
		return
	}
	sw.logger.Info("Logical CDC lock acquired on task via REST", "task_id", record.ID, "topic", record.TopicName)

	// 2. Resolve variables and business key
	if msg.Data.Metadata.Enrichment.ID != "" {
		sw.logger.Info("CDC mode activated: using enriched metadata (zero-lookup for variables)", "task_id", record.ID, "business_key", msg.Data.Metadata.Enrichment.BusinessKey)

		// Extract variables from enrichment
		variables = make(map[string]Variable)
		for k, v := range msg.Data.Metadata.Enrichment.Variables {
			variables[k] = Variable{
				Value: v.Value,
			}
		}

		// Use business key from enrichment
		businessKey = msg.Data.Metadata.Enrichment.BusinessKey
	} else {
		// Legacy-mode: Fetch variables and business key via REST API
		var getErr error
		variables, getErr = sw.client.GetExecutionVariables(ctx, record.ExecutionID)
		if getErr != nil {
			sw.logger.Error("Failed to fetch execution variables for task via REST", "task_id", record.ID, "error", getErr)
			_ = sw.client.Unlock(record.ID).Context(context.Background()).Execute()
			sw.nackMessage(context.Background(), msg.AckID)
			return
		}

		if businessKey == "" {
			bk, err := sw.client.GetProcessInstanceBusinessKey(ctx, record.ProcInstID)
			if err == nil {
				businessKey = bk
			} else {
				sw.logger.Warn("Failed to fetch process instance business key", "proc_inst_id", record.ProcInstID, "error", err)
			}
		}
	}

	// Construct ExternalTask
	task := ExternalTask{
		ID:                 record.ID,
		TopicName:          record.TopicName,
		WorkerID:           sw.workerID,
		ProcessInstanceID:  record.ProcInstID,
		ExecutionID:        record.ExecutionID,
		BusinessKey:        businessKey,
		Variables:          variables,
		LockExpirationTime: lockExpiration,
	}

	// Create complete and fail builders
	complete := func() *TaskCompletion {
		return NewTaskCompletion(sw.client.httpClient, sw.workerID, record.ID).Context(ctx).Logger(sw.logger)
	}

	fail := func(errorMessage, errorDetails string, retries, retryTimeout int) error {
		return NewTaskFailure(sw.client.httpClient, sw.workerID, record.ID).
			Context(ctx).
			ErrorMessage(errorMessage).
			ErrorDetails(errorDetails).
			Retries(retries).
			RetryTimeout(retryTimeout).
			Execute()
	}

	// Execute handler
	if err := handler.Handle(ctx, sw.client, task, complete, fail); err != nil {
		if errors.Is(err, ErrTaskDelegated) {
			sw.logger.Info("Task delegated asynchronously, freeing worker thread", "task_id", record.ID)
			sw.ackMessage(context.Background(), msg.AckID)
			return
		}

		sw.logger.Error("Task handler returned error", "task_id", record.ID, "error", err)
		if strings.Contains(err.Error(), "OptimisticLockingException") {
			backoff := time.Duration(500+rnd.Intn(1000)) * time.Millisecond
			sw.logger.Warn("Optimistic locking collision, backing off, unlocking and nacking in Sequin", "task_id", record.ID, "backoff", backoff)

			// Unlock in Camunda to reset lock owner/expiration
			_ = sw.client.Unlock(record.ID).Context(context.Background()).Execute()

			time.Sleep(backoff)
			sw.nackMessage(context.Background(), msg.AckID)
		} else {
			_ = fail(err.Error(), "Handler error", 0, 0)
			sw.ackMessage(context.Background(), msg.AckID)
		}
	} else {
		sw.logger.Info("Task processed successfully via Sequin", "task_id", record.ID)
		sw.ackMessage(context.Background(), msg.AckID)
	}
}
