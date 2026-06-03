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

	submissionDelayMs := 5
	if v := os.Getenv("CAMUNDA_SUBMISSION_DELAY_MS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			submissionDelayMs = parsed
		}
	}

	appsCount := 5
	if v := os.Getenv("CAMUNDA_APPLICATIONS_COUNT"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			appsCount = parsed
		}
	}

	sequinURL := os.Getenv("SEQUIN_URL")
	if sequinURL == "" {
		sequinURL = "http://localhost:7376"
	}
	sequinConsumer := os.Getenv("SEQUIN_CONSUMER")
	if sequinConsumer == "" {
		sequinConsumer = "camunda_tasks_stream"
	}

	// Camunda client (sets workerID to match trigger auto-lock: "loan-worker-cdc")
	client, err := camunda.NewClient("http://localhost:8080", "loan-worker-cdc")
	if err != nil {
		logger.Error("Failed to create client", "error", err)
		return
	}
	client.Use(httpstream.LoggingMiddleware(logger))

	// Deploy BPMN schema
	if err := deployProcess(ctx, client, logger); err != nil {
		logger.Error("Failed to deploy BPMN process", "error", err)
		return
	}

	store := storepkg.New()

	// Simulate loan applications submission
	go func() {
		logger.Info("Simulating external loan applications...")
		for i := 1; i <= appsCount; i++ {
			businessKey := "loan-cdc-enrich-" + uuid.NewString()
			app := storepkg.Application{
				ApplicationNumber: i,
				ApplicantName:     fmt.Sprintf("Applicant CDC Enrich %d", i),
				ApplicantEmail:    fmt.Sprintf("app-cdc-enrich%d@example.com", i),
				RequestedAmount:   5000.0,
				LoanPurpose:       "CDC SQL Enrichment Test",
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
			time.Sleep(time.Duration(submissionDelayMs) * time.Millisecond)
		}
		logger.Info("All loan applications submitted successfully")
	}()

	// Configure handlers
	creditScoreChecker := handlers.NewCreditScoreChecker(logger, store)
	loanGranter := handlers.NewLoanGranter(logger, store)
	requestRejecter := handlers.NewRequestRejecter(logger, store)
	decider := handlers.NewDecider(logger, store)

	// Create and start our standard SequinWorker (which now supports zero-lookup CDC)
	w, err := camunda.NewSequinWorker(client, sequinURL, sequinConsumer, logger)
	if err != nil {
		logger.Error("Failed to create Sequin worker", "error", err)
		return
	}
	w.RegisterHandler("creditScoreChecker", creditScoreChecker)
	w.RegisterHandler("decider", decider)
	w.RegisterHandler("loanGranter", loanGranter)
	w.RegisterHandler("requestRejecter", requestRejecter)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("Shutdown signal received, stopping worker...")
		cancel()
	}()

	w.Start(ctx)
	logger.Info("Worker stopped gracefully")
}

func startLoanApplication(ctx context.Context, client *camunda.Client, logger *slog.Logger, businessKey string) error {
	processInstanceID, err := client.StartProcessInstance(ctx, "loan_process", businessKey, nil)
	if err != nil {
		return err
	}
	logger.Info("Loan application started", "businessKey", businessKey, "processInstanceID", processInstanceID)
	return nil
}

func deployProcess(ctx context.Context, client *camunda.Client, logger *slog.Logger) error {
	file, err := os.Open("bpmn/loan-granting.bpmn")
	if err != nil {
		return err
	}
	defer file.Close()

	deploymentID, err := client.DeployProcess(ctx, "loan-granting-cdc-outbox-deployment", file, file.Name())
	if err != nil {
		return err
	}
	logger.Info("Deployed BPMN process def", "deploymentID", deploymentID)
	return nil
}
