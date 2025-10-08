package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nativebpm/camunda"
)

// LoanGranter handles loan granting tasks
type LoanGranter struct {
	logger *slog.Logger
}

// NewLoanGranter creates a new loan granter handler
func NewLoanGranter(logger *slog.Logger) *LoanGranter {
	return &LoanGranter{
		logger: logger,
	}
}

// Handle processes a loan granting task
func (h *LoanGranter) Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask) error {
	h.logger.Info("Processing loan grant", "taskID", task.ID, "processInstanceID", task.ProcessInstanceID)

	// Extract credit score from task variables (provided by multi-instance subprocess)
	scoreVar, ok := task.Variables["score"]
	if !ok {
		return fmt.Errorf("score variable not found in task")
	}

	score, ok := scoreVar.Value.(float64)
	if !ok {
		return fmt.Errorf("score is not a number: %T", scoreVar.Value)
	}

	// Extract requested amount from process variables
	var requestedAmount float64
	if amountVar, ok := task.Variables["requestedAmount"]; ok {
		if val, ok := amountVar.Value.(float64); ok {
			requestedAmount = val
		}
	}

	// Extract applicant name for logging
	var applicantName string
	if nameVar, ok := task.Variables["applicantName"]; ok {
		if val, ok := nameVar.Value.(string); ok {
			applicantName = val
		}
	}

	h.logger.Info("Evaluating loan approval",
		"applicantName", applicantName,
		"requestedAmount", requestedAmount,
		"creditScore", score,
		"taskID", task.ID)

	// Simulate loan processing (background checks, verification, etc.)
	time.Sleep(2 * time.Second)

	// Calculate approved amount based on credit score and requested amount
	// Higher score = higher approval percentage
	approvalPercentage := (score / 10.0) * 100.0 // score 7 = 70%, score 8 = 80%
	approvedAmount := requestedAmount * (approvalPercentage / 100.0)

	// Calculate interest rate based on credit score
	// Better score = lower interest rate
	interestRate := 15.0 - (score - 5.0) // score 7 = 13%, score 8 = 12%, score 9 = 11%
	if interestRate < 5.0 {
		interestRate = 5.0 // Minimum 5%
	}

	h.logger.Info("Loan approved",
		"requestedAmount", requestedAmount,
		"approvedAmount", approvedAmount,
		"interestRate", interestRate,
		"creditScore", score)

	// Complete the task with results
	variables := map[string]camunda.Variable{
		"loanGranted":     camunda.BooleanVariable(true),
		"approvedAmount":  camunda.DoubleVariable(approvedAmount),
		"interestRate":    camunda.DoubleVariable(interestRate),
		"approvalMessage": camunda.StringVariable(fmt.Sprintf("Congratulations! Your loan of $%.2f has been approved at %.2f%% interest rate.", approvedAmount, interestRate)),
	}

	err := client.Complete(task.ID).
		Context(ctx).
		Variables(variables).
		Execute()
	if err != nil {
		return err
	}

	h.logger.Info("Loan granted successfully",
		"taskID", task.ID,
		"approvedAmount", approvedAmount,
		"interestRate", interestRate)
	return nil
}
