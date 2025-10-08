package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nativebpm/camunda"
	"github.com/nativebpm/camunda/examples/loan-granting/handlers"
)

func main() {
	logger := slog.Default()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a new Camunda client
	client, err := camunda.NewClient("http://localhost:8080", "loan-worker")
	if err != nil {
		logger.Error("Failed to create client", "error", err)
		return
	}

	// Add logging middleware
	client.WithLogger(logger)

	// Deploy the BPMN process
	if err := deployProcess(ctx, client, logger); err != nil {
		logger.Error("Failed to deploy process", "error", err)
		return
	}

	// Simulate external requests (3 loan applications)
	logger.Info("Simulating external loan applications...")
	for i := 1; i <= 3; i++ {
		if err := startLoanApplication(ctx, client, logger, i); err != nil {
			logger.Error("Failed to start loan application", "number", i, "error", err)
		}
		time.Sleep(500 * time.Millisecond) // Small delay between applications
	}
	logger.Info("All loan applications submitted")

	// Create and configure the worker
	w := createWorker(client, logger)

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

// startLoanApplication simulates an external loan application request
// In a real system, this would be triggered by an API call, message queue, etc.
func startLoanApplication(ctx context.Context, client *camunda.Client, logger *slog.Logger, applicationNumber int) error {
	// Prepare loan application data
	// These variables will be available throughout the process execution
	variables := map[string]any{
		// Application metadata
		"applicationNumber": applicationNumber,
		"applicantName":     fmt.Sprintf("John Doe %d", applicationNumber),
		"applicantEmail":    fmt.Sprintf("applicant%d@example.com", applicationNumber),

		// Loan details
		"requestedAmount": float64(10000 + (applicationNumber * 5000)), // Varying amounts: 15k, 20k, 25k
		"loanPurpose":     "Business expansion",
		"loanTerm":        36, // months

		// Applicant financial data (used by creditScoreChecker)
		"monthlyIncome":   float64(5000 + (applicationNumber * 1000)),
		"existingDebts":   float64(2000 * applicationNumber),
		"employmentYears": 3 + applicationNumber,

		// Application timestamp
		"submittedAt": time.Now().Format(time.RFC3339),
	}

	processInstanceID, err := client.StartProcessInstance(ctx, "loan_process", variables)
	if err != nil {
		return err
	}

	logger.Info("Loan application received",
		"applicationNumber", applicationNumber,
		"applicantName", variables["applicantName"],
		"requestedAmount", variables["requestedAmount"],
		"processInstanceID", processInstanceID)
	return nil
}

// createWorker creates and configures the external task worker
func createWorker(client *camunda.Client, logger *slog.Logger) *camunda.Worker {
	// Create handlers
	creditScoreChecker := handlers.NewCreditScoreChecker(logger)
	loanGranter := handlers.NewLoanGranter(logger)
	requestRejecter := handlers.NewRequestRejecter(logger)

	// Create worker and register handlers
	w := camunda.NewWorker(client, logger)
	w.RegisterHandler("creditScoreChecker", creditScoreChecker, 60000, []string{})
	w.RegisterHandler("loanGranter", loanGranter, 60000, []string{"score", "applicantName", "requestedAmount"})
	w.RegisterHandler("requestRejecter", requestRejecter, 60000, []string{"score", "applicantName", "requestedAmount"})
	w.SetMaxTasks(10)
	w.SetPollInterval(5 * time.Second)

	logger.Info("Worker configured",
		"topics", 3,
		"maxTasks", 10,
		"pollInterval", "5s")

	return w
}
