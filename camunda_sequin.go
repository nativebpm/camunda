package camunda

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/nativebpm/camunda/internal/tasks"
)

// SequinWorker processes Camunda external tasks using logical replication logs from Sequin
// instead of polling via fetchAndLock. It performs atomic direct DB locks to achieve high-performance CDC.
type SequinWorker struct {
	client     *Client
	db         *sql.DB
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
func NewSequinWorker(client *Client, dbDSN string, sequinURL string, consumer string, logger *slog.Logger) (*SequinWorker, error) {
	if logger == nil {
		logger = slog.Default()
	}

	db, err := sql.Open("postgres", dbDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Camunda DB: %w", err)
	}

	// Optimize DB connection pool
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping Camunda DB: %w", err)
	}

	token := os.Getenv("SEQUIN_API_TOKEN")
	if token == "" {
		token = "sequin_loadtest_secret_token_12345"
	}

	return &SequinWorker{
		client:     client,
		db:         db,
		sequinURL:  sequinURL,
		consumer:   consumer,
		apiToken:   token,
		logger:     logger,
		handlers:   make(map[string]TaskHandler),
		workerID:   client.workerID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
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
			sw.db.Close()
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

func (sw *SequinWorker) processMessage(ctx context.Context, msg sequinMessage) {
	// Always ACK the message from Sequin at the end to prevent redelivery
	defer sw.ackMessage(context.Background(), msg.AckID)

	if msg.Payload.Action == "delete" {
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
		return
	}

	handler, ok := sw.handlers[topicName]
	if !ok {
		// No handler registered for this topic
		return
	}

	// 1. Try to lock the task directly in the database (logical locking mechanism)
	lockDuration := 5 * time.Minute
	lockExpiration := time.Now().Add(lockDuration)

	// In Camunda 7 Postgres schema, column names are id_, topic_name_, lock_exp_time_, worker_id_, retries_
	query := `
		UPDATE act_ru_ext_task 
		SET lock_exp_time_ = $1, 
		    worker_id_ = $2, 
		    retries_ = $3 
		WHERE id_ = $4 AND (lock_exp_time_ IS NULL OR lock_exp_time_ < NOW())`

	res, err := sw.db.ExecContext(ctx, query, lockExpiration, sw.workerID, 3, taskID)
	if err != nil {
		sw.logger.Error("Failed to lock task in Camunda DB", "task_id", taskID, "error", err)
		return
	}

	rows, err := res.RowsAffected()
	if err != nil {
		sw.logger.Error("Failed to get rows affected during lock", "task_id", taskID, "error", err)
		return
	}
	if rows == 0 {
		sw.logger.Warn("Task already locked or completed (0 rows affected)", "task_id", taskID)
		return
	}


	sw.logger.Info("Logical CDC lock acquired on task", "task_id", taskID, "topic", topicName)

	// 2. Query process variables directly from the DB to build type-safe ExternalTask variables
	variables, err := sw.queryVariables(ctx, procInstID, executionID)
	if err != nil {
		sw.logger.Error("Failed to fetch process variables for task", "task_id", taskID, "error", err)
		// Try to unlock so it can be retried
		_, _ = sw.db.ExecContext(context.Background(), "UPDATE act_ru_ext_task SET lock_exp_time_ = NULL, worker_id_ = NULL WHERE id_ = $1", taskID)
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
		_ = fail(err.Error(), "Handler error", 0, 0)
	} else {
		sw.logger.Info("Task processed successfully via Sequin", "task_id", taskID)
	}
}

func (sw *SequinWorker) queryVariables(ctx context.Context, procInstID, executionID string) (map[string]Variable, error) {
	// Query variables from act_ru_variable. We pull both process instance scope and execution local scope
	query := `
		SELECT name_, type_, text_, double_, long_ 
		FROM act_ru_variable 
		WHERE proc_inst_id_ = $1 OR execution_id_ = $2`

	rows, err := sw.db.QueryContext(ctx, query, procInstID, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	variables := make(map[string]Variable)
	for rows.Next() {
		var name, varType string
		var textVal sql.NullString
		var doubleVal sql.NullFloat64
		var longVal sql.NullInt64

		if err := rows.Scan(&name, &varType, &textVal, &doubleVal, &longVal); err != nil {
			return nil, err
		}

		var value any
		switch varType {
		case "string":
			if textVal.Valid {
				value = textVal.String
			}
		case "integer", "long":
			if longVal.Valid {
				value = float64(longVal.Int64) // JSON variables wrapper parses integers as float64
			}
		case "double":
			if doubleVal.Valid {
				value = doubleVal.Float64
			}
		case "boolean":
			if longVal.Valid {
				value = longVal.Int64 == 1
			}
		default:
			// Fallback text
			if textVal.Valid {
				value = textVal.String
			}
		}

		variables[name] = Variable{
			Type:  varType,
			Value: value,
		}
	}

	return variables, nil
}
