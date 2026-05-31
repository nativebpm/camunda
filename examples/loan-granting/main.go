package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
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
	client, err := camunda.NewClient("http://localhost:8080", "loan-worker", logger)
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

	go func() {
		// Simulate external requests
		logger.Info("Simulating external loan applications...")
		for i := 1; i <= 1000; i++ {
			if err := startLoanApplication(ctx, client, logger, i); err != nil {
				logger.Error("Failed to start loan application", "number", i, "error", err)
			}
		}
		logger.Info("All loan applications submitted")
	}()

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
	// Prepare loan application data with more realistic variation
	applicantNames := []string{"John Doe", "Jane Smith", "Alex Johnson", "Maria Garcia", "Li Wei", "Fatima Al-Farsi", "Ivan Petrov", "Sara Müller"}
	loanPurposes := []string{"Business expansion", "Home renovation", "Car purchase", "Education", "Medical expenses", "Travel", "Debt consolidation", "Wedding"}

	name := applicantNames[applicationNumber%len(applicantNames)]
	email := fmt.Sprintf("%s%d@example.com", name[:3], applicationNumber)
	purpose := loanPurposes[applicationNumber%len(loanPurposes)]

	// Vary requested amount, income, debts, employment years
	requestedAmount := float64(5000 + (applicationNumber%10)*2500 + (applicationNumber%3)*10000)
	monthlyIncome := float64(3000 + (applicationNumber%7)*1200 + (applicationNumber%5)*800)
	existingDebts := float64((applicationNumber%5)*3500 + (applicationNumber%4)*1200)
	employmentYears := 1 + (applicationNumber % 12)
	loanTerm := 12 + (applicationNumber%5)*12 // 12, 24, 36, 48, 60 months

	vars := camunda.NewVariables()
	// Application metadata
	vars.Int("applicationNumber", applicationNumber)
	vars.String("applicantName", fmt.Sprintf("%s %d", name, applicationNumber))
	vars.String("applicantEmail", email)

	// Loan details
	vars.Double("requestedAmount", requestedAmount)
	vars.String("loanPurpose", purpose)
	vars.Int("loanTerm", loanTerm)

	// Applicant financial data (used by creditScoreChecker)
	vars.Double("monthlyIncome", monthlyIncome)
	vars.Double("existingDebts", existingDebts)
	vars.Int("employmentYears", employmentYears)

	// Application timestamp
	vars.Date("submittedAt", time.Now())

	variables := vars.Variables()
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
	// Recommended: keep maxTasks in the 10-50 range depending on workload
	w.SetMaxTasks(50)

	// Enable long polling to reduce fetch/load on the REST API
	w.SetAsyncResponseTimeout(20 * time.Second)

	// Short poll interval fallback when no tasks are returned
	w.SetPollInterval(1 * time.Second)

	// Configure concurrency control
	numCPU := runtime.NumCPU() / 2
	w.SetMaxConcurrency(numCPU) // Use half of available CPU cores

	logger.Info("Worker configured",
		"topics", 3,
		"maxTasks", 50,
		"asyncResponseTimeout", "20s",
		"pollInterval", "1s",
		"maxConcurrency", numCPU)

	return w
}
