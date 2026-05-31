package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nativebpm/camunda"
	storepkg "github.com/nativebpm/camunda/examples/loan-granting/store"
)

// RequestRejecter handles loan rejection tasks
type RequestRejecter struct {
	logger *slog.Logger
	store  *storepkg.Store
}

// NewRequestRejecter creates a new request rejecter handler
func NewRequestRejecter(logger *slog.Logger, store *storepkg.Store) *RequestRejecter {
	return &RequestRejecter{
		logger: logger,
		store:  store,
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
	if applicantName == "" && task.BusinessKey != "" && h.store != nil {
		if app, ok := h.store.Get(task.BusinessKey); ok {
			applicantName = app.ApplicantName
			if requestedAmount == 0 {
				requestedAmount = app.RequestedAmount
			}
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
	var reason string

	if score <= 3 {
		reason = fmt.Sprintf("Credit score of %.1f is significantly below our minimum requirement of 5.0", score)
	} else if score <= 5 {
		reason = fmt.Sprintf("Credit score of %.1f does not meet our minimum requirement of 5.0", score)
	} else {
		// This shouldn't happen (gateway condition is score <= 5), but handle it
		reason = fmt.Sprintf("Unable to approve loan at this time (score: %.1f)", score)
	}

	h.logger.Info("Loan rejected",
		"creditScore", score,
		"reason", reason)

	// Save rejection result to in-memory store instead of writing variables to Camunda
	if task.BusinessKey != "" && h.store != nil {
		res := storepkg.Result{
			Score:          int(score),
			LoanGranted:    false,
			ApprovedAmount: 0,
			InterestRate:   0,
			Message:        fmt.Sprintf("Rejection: %s", reason),
		}
		h.store.AppendResult(task.BusinessKey, res)
	}

	// Complete the task without writing extra variables to Camunda
	if err := complete().Execute(); err != nil {
		return err
	}

	h.logger.Info("Loan request result saved to in-memory DB",
		"taskID", task.ID,
		"reason", reason)
	return nil
}
