package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nativebpm/camunda"
	"github.com/nativebpm/camunda/examples/loan-granting/handlers"
	storepkg "github.com/nativebpm/camunda/examples/loan-granting/store"
	"github.com/nativebpm/httpstream"
)

func main() {
	logger := slog.Default()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Make submission throttle configurable via env var
	submissionDelayMs := 5 // default
	if v := os.Getenv("CAMUNDA_SUBMISSION_DELAY_MS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			submissionDelayMs = parsed
		} else {
			logger.Warn("Invalid CAMUNDA_SUBMISSION_DELAY_MS, using default", "value", v)
		}
	}
	logger.Info("Submission throttle configured", "delay_ms", submissionDelayMs)

	// Create a new Camunda client
	client, err := camunda.NewClient("http://localhost:8080", "loan-worker")
	if err != nil {
		logger.Error("Failed to create client", "error", err)
		return
	}

	client.Use(httpstream.LoggingMiddleware(logger))

	// Deploy the BPMN process
	if err := deployProcess(ctx, client, logger); err != nil {
		logger.Error("Failed to deploy process", "error", err)
		return
	}

	// Create in-memory store for application payloads (not stored in Camunda)
	store := storepkg.New()

	go func() {
		// Simulate external requests with throttling to avoid DB contention
		logger.Info("Simulating external loan applications (throttled)...")
		for i := 1; i <= 1000; i++ {
			// Build application object and save to in-memory store under a businessKey
			// Use a UUID to ensure globally-unique business keys
			businessKey := "loan-" + uuid.NewString()
			app := storepkg.Application{
				ApplicationNumber: i,
				ApplicantName:     fmt.Sprintf("Applicant %d", i),
				ApplicantEmail:    fmt.Sprintf("app%d@example.com", i),
				RequestedAmount:   5000.0,
				LoanPurpose:       "General",
				LoanTerm:          12,
				MonthlyIncome:     3000.0,
				ExistingDebts:     0.0,
				EmploymentYears:   1,
				SubmittedAtUnix:   time.Now().Unix(),
			}
			store.Save(businessKey, app)

			if err := startLoanApplication(ctx, client, logger, businessKey); err != nil {
				logger.Error("Failed to start loan application", "number", i, "businessKey", businessKey, "error", err)
			}

			// Small sleep between submissions to reduce burst load on Camunda/DB
			// Adjustable via CAMUNDA_SUBMISSION_DELAY_MS (milliseconds)
			time.Sleep(time.Duration(submissionDelayMs) * time.Millisecond)
		}
		logger.Info("All loan applications submitted")
	}()

	// Create and configure the worker (pass the in-memory store so handlers can fetch data)
	w := createWorker(client, logger, store)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("Shutdown signal received, stopping worker...")
		cancel()
	}()

	// Start the worker (blocking call)
	logger.Info("Starting external task worker... Press Ctrl+C to stop")
	w.Start(ctx)

	// Worker stopped gracefully
	logger.Info("Worker stopped gracefully")
}

// startLoanApplication starts the process with only a businessKey.
// Heavy application data is stored in the in-memory store and not in Camunda variables.
func startLoanApplication(ctx context.Context, client *camunda.Client, logger *slog.Logger, businessKey string) error {
	// Start process with businessKey only (no large variables)
	processInstanceID, err := client.StartProcessInstance(ctx, "loan_process", businessKey, nil)
	if err != nil {
		return err
	}

	logger.Info("Loan application started",
		"businessKey", businessKey,
		"processInstanceID", processInstanceID)
	return nil
}

// deployProcess deploys the BPMN process definition
func deployProcess(ctx context.Context, client *camunda.Client, logger *slog.Logger) error {
	file, err := os.Open("bpmn/loan-granting.bpmn")
	if err != nil {
		return err
	}
	defer file.Close()

	deploymentID, err := client.DeployProcess(ctx, "loan-granting-deployment", file, file.Name())
	if err != nil {
		return err
	}

	logger.Info("Deployed BPMN process", "deploymentID", deploymentID)
	return nil
}

// createWorker creates and configures the external task worker
func createWorker(client *camunda.Client, logger *slog.Logger, store *storepkg.Store) *camunda.Worker {
	// Create handlers
	creditScoreChecker := handlers.NewCreditScoreChecker(logger, store)
	loanGranter := handlers.NewLoanGranter(logger, store)
	requestRejecter := handlers.NewRequestRejecter(logger, store)
	decider := handlers.NewDecider(logger, store)

	// Create worker and register handlers
	w := camunda.NewWorker(client, logger)
	w.RegisterHandler("creditScoreChecker", creditScoreChecker, 60000, []string{})
	// Decider makes a decision based on scores in the external store and sets a small process variable `decision`
	w.RegisterHandler("decider", decider, 60000, []string{})
	// Grant/reject tasks will be executed after the decision; they only need the `decision` variable if desired
	w.RegisterHandler("loanGranter", loanGranter, 60000, []string{"decision"})
	w.RegisterHandler("requestRejecter", requestRejecter, 60000, []string{"decision"})
	// Recommended: keep maxTasks in the 10-50 range depending on workload
	w.SetMaxTasks(50)

	// Enable long polling to reduce fetch/load on the REST API
	w.SetAsyncResponseTimeout(20 * time.Second)

	// Short poll interval fallback when no tasks are returned
	w.SetPollInterval(1 * time.Second)

	logger.Info("Worker configured",
		"topics", 3,
		"maxTasks", 50,
		"asyncResponseTimeout", "20s",
		"pollInterval", "1s")

	return w
}
