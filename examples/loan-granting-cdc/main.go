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

	// Make applications count configurable via env var
	appsCount := 5 // default for quick testing/debugging
	if v := os.Getenv("CAMUNDA_APPLICATIONS_COUNT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			appsCount = parsed
		} else {
			logger.Warn("Invalid CAMUNDA_APPLICATIONS_COUNT, using default", "value", v)
		}
	}
	logger.Info("Workload configured", "applications_count", appsCount)

	// Configure Sequin connection
	sequinURL := os.Getenv("SEQUIN_URL")
	if sequinURL == "" {
		sequinURL = "http://localhost:7376"
	}
	sequinConsumer := os.Getenv("SEQUIN_CONSUMER")
	if sequinConsumer == "" {
		sequinConsumer = "camunda_tasks_stream"
	}

	// Create a new Camunda client
	client, err := camunda.NewClient("http://localhost:8080", "loan-worker-cdc")
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
		for i := 1; i <= appsCount; i++ {
			// Build application object and save to in-memory store under a businessKey
			businessKey := "loan-cdc-" + uuid.NewString()
			app := storepkg.Application{
				ApplicationNumber: i,
				ApplicantName:     fmt.Sprintf("Applicant CDC %d", i),
				ApplicantEmail:    fmt.Sprintf("app-cdc%d@example.com", i),
				RequestedAmount:   5000.0,
				LoanPurpose:       "General CDC",
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
			time.Sleep(time.Duration(submissionDelayMs) * time.Millisecond)
		}
		logger.Info("All loan applications submitted")
	}()

	// Create and configure the Sequin worker
	w, err := createSequinWorker(client, sequinURL, sequinConsumer, logger, store)
	if err != nil {
		logger.Error("Failed to create Sequin worker", "error", err)
		return
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("Shutdown signal received, stopping worker...")
		cancel()
	}()

	// Start the worker (blocking call)
	logger.Info("Starting Sequin CDC task worker... Press Ctrl+C to stop")
	w.Start(ctx)

	// Worker stopped gracefully
	logger.Info("Worker stopped gracefully")
}

// startLoanApplication starts the process with only a businessKey.
func startLoanApplication(ctx context.Context, client *camunda.Client, logger *slog.Logger, businessKey string) error {
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

	deploymentID, err := client.DeployProcess(ctx, "loan-granting-cdc-deployment", file, file.Name())
	if err != nil {
		return err
	}

	logger.Info("Deployed BPMN process", "deploymentID", deploymentID)
	return nil
}

// createSequinWorker creates and configures the external task worker on top of Sequin CDC
func createSequinWorker(client *camunda.Client, sequinURL string, consumer string, logger *slog.Logger, store *storepkg.Store) (*camunda.SequinWorker, error) {
	// Create handlers (reusing the ones from loan-granting)
	creditScoreChecker := handlers.NewCreditScoreChecker(logger, store)
	loanGranter := handlers.NewLoanGranter(logger, store)
	requestRejecter := handlers.NewRequestRejecter(logger, store)
	decider := handlers.NewDecider(logger, store)

	// Create Sequin CDC worker
	w, err := camunda.NewSequinWorker(client, sequinURL, consumer, logger)
	if err != nil {
		return nil, err
	}

	// Register handlers with 60s lock duration
	w.RegisterHandlerWithOptions("creditScoreChecker", creditScoreChecker, 60000)
	w.RegisterHandlerWithOptions("decider", decider, 60000)
	w.RegisterHandlerWithOptions("loanGranter", loanGranter, 60000)
	w.RegisterHandlerWithOptions("requestRejecter", requestRejecter, 60000)

	// Set worker concurrency
	w.SetMaxConcurrency(20)

	logger.Info("Sequin CDC Worker configured",
		"sequinURL", sequinURL,
		"consumer", consumer,
		"maxConcurrency", 20)

	return w, nil
}
