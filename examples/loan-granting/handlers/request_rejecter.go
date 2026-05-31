package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nativebpm/camunda"
)

// RequestRejecter handles loan rejection tasks
type RequestRejecter struct {
	logger *slog.Logger
}

// NewRequestRejecter creates a new request rejecter handler
func NewRequestRejecter(logger *slog.Logger) *RequestRejecter {
	return &RequestRejecter{
		logger: logger,
	}
}

// Handle processes a loan rejection task
func (h *RequestRejecter) Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
	h.logger.Info("Processing loan rejection", "taskID", task.ID, "processInstanceID", task.ProcessInstanceID)

	// Extract credit score from task variables (provided by multi-instance subprocess)
	scoreVar, ok := task.Variables["score"]
	if !ok {
		return fmt.Errorf("score variable not found in task")
	}

	score, ok := scoreVar.Value.(float64)
	if !ok {
		return fmt.Errorf("score is not a number: %T", scoreVar.Value)
	}

	// Extract applicant data from process variables
	var applicantName string
	var requestedAmount float64

	if nameVar, ok := task.Variables["applicantName"]; ok {
		if val, ok := nameVar.Value.(string); ok {
			applicantName = val
		}
	}
	if amountVar, ok := task.Variables["requestedAmount"]; ok {
		if val, ok := amountVar.Value.(float64); ok {
			requestedAmount = val
		}
	}

	h.logger.Info("Evaluating loan rejection",
		"applicantName", applicantName,
		"requestedAmount", requestedAmount,
		"creditScore", score,
		"taskID", task.ID)

	// Simulate rejection processing (notification, compliance checks, etc.)
	//time.Sleep(2 * time.Second)

	// Prepare detailed rejection reason based on credit score
	var reason, recommendation string

	if score <= 3 {
		reason = fmt.Sprintf("Credit score of %.1f is significantly below our minimum requirement of 5.0", score)
		recommendation = "We recommend working on improving your credit score by paying existing debts and maintaining consistent employment"
	} else if score <= 5 {
		reason = fmt.Sprintf("Credit score of %.1f does not meet our minimum requirement of 5.0", score)
		recommendation = "You may reapply after 6 months. Consider reducing existing debts to improve your credit score"
	} else {
		// This shouldn't happen (gateway condition is score <= 5), but handle it
		reason = fmt.Sprintf("Unable to approve loan at this time (score: %.1f)", score)
		recommendation = "Please contact our support team for more information"
	}

	h.logger.Info("Loan rejected",
		"creditScore", score,
		"reason", reason)

	// Complete the task with results
	// Use provided complete factory for fluent completion
	err := complete().
		BooleanVariable("loanRejected", true).
		StringVariable("rejectionReason", reason).
		StringVariable("recommendation", recommendation).
		StringVariable("rejectionMessage", fmt.Sprintf("We're sorry, but we cannot approve your loan application for $%.2f. %s", requestedAmount, reason)).
		StringVariable("canReapplyAfter", "6 months").
		Execute()
	if err != nil {
		return err
	}

	h.logger.Info("Loan request rejected",
		"taskID", task.ID,
		"reason", reason,
		"recommendation", recommendation)
	return nil
}
