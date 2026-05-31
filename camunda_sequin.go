package camunda

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
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
	consumer       string
	logger         *slog.Logger
	handlers       map[string]TaskHandler
	workerID       string
	wg             sync.WaitGroup
	maxConcurrency int
	taskSemaphore  chan struct{}
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

	return &SequinWorker{
		client:         client,
		sequinClient:   sequinClient,
		consumer:       consumer,
		logger:         logger,
		handlers:       make(map[string]TaskHandler),
		workerID:       client.workerID,
		maxConcurrency: maxConcurrency,
		taskSemaphore:  make(chan struct{}, maxConcurrency),
	}, nil
}

// RegisterHandler registers a task handler for a topic
func (sw *SequinWorker) RegisterHandler(topicName string, handler TaskHandler) *SequinWorker {
	sw.handlers[topicName] = handler
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
			sw.logger.Info("Context cancelled, waiting for active tasks to complete...")
			sw.wg.Wait()
			return
		default:
			// 1. Calculate free slots in our concurrency semaphore
			freeSlots := sw.maxConcurrency - len(sw.taskSemaphore)
			if freeSlots <= 0 {
				// Semaphore is fully saturated. Block until at least one worker slot is freed.
				select {
				case <-ctx.Done():
					sw.logger.Info("Context cancelled, waiting for active tasks to complete...")
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
					sw.logger.Info("Context cancelled, waiting for active tasks to complete...")
					sw.wg.Wait()
					return
				case sw.taskSemaphore <- struct{}{}:
				}

				sw.wg.Add(1)
				go func(m sequin.Message) {
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

func (sw *SequinWorker) receiveMessages(ctx context.Context, batchSize int) ([]sequin.Message, error) {
	return sw.sequinClient.Receive(ctx, sw.consumer, &sequin.ReceiveParams{
		BatchSize: batchSize,
		WaitFor:   5000, // 5s long poll
	})
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

func (sw *SequinWorker) processMessage(ctx context.Context, msg sequin.Message) {
	var record extTaskRecord
	if err := json.Unmarshal(msg.Record, &record); err != nil {
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

	// 1. Lock the task using Camunda REST API
	lockDurationMs := 30000 // 30 seconds
	lockExpiration := time.Now().Add(30 * time.Second)

	err := sw.client.Lock(record.ID, lockDurationMs).Context(ctx).Execute()
	if err != nil {
		// If the lock failed (e.g. task was already completed or deleted in Camunda)
		// we should ack the message to discard it.
		sw.logger.Warn("Failed to acquire lock via REST API (task might be completed, deleted, or locked)", "task_id", record.ID, "error", err)
		sw.ackMessage(context.Background(), msg.AckID)
		return
	}

	sw.logger.Info("Logical CDC lock acquired on task via REST", "task_id", record.ID, "topic", record.TopicName)

	// 2. Query process variables via Camunda REST API
	variables, err := sw.client.GetProcessVariables(ctx, record.ProcInstID)
	if err != nil {
		sw.logger.Error("Failed to fetch process variables for task via REST", "task_id", record.ID, "error", err)
		sw.nackMessage(context.Background(), msg.AckID)
		return
	}

	// 3. Construct ExternalTask
	task := ExternalTask{
		ID:                  record.ID,
		TopicName:           record.TopicName,
		WorkerID:            sw.workerID,
		ProcessInstanceID:   record.ProcInstID,
		ExecutionID:         record.ExecutionID,
		BusinessKey:         record.BusinessKey,
		Variables:           variables,
		LockExpirationTime:  &lockExpiration,
	}

	// 4. Create complete and fail builders
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

	// 5. Execute handler
	if err := handler.Handle(ctx, sw.client, task, complete, fail); err != nil {
		sw.logger.Error("Task handler returned error", "task_id", record.ID, "error", err)
		if strings.Contains(err.Error(), "OptimisticLockingException") {
			backoff := time.Duration(500+rnd.Intn(1000)) * time.Millisecond
			sw.logger.Warn("Optimistic locking collision, backing off and nacking in Sequin", "task_id", record.ID, "backoff", backoff)
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
