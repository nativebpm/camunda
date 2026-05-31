package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nativebpm/camunda"
	storepkg "github.com/nativebpm/camunda/examples/loan-granting/store"
)

// Decider reads scores from the in-memory store and sets a decision variable
type Decider struct {
	logger *slog.Logger
	store  *storepkg.Store
}

func NewDecider(logger *slog.Logger, store *storepkg.Store) *Decider {
	return &Decider{logger: logger, store: store}
}

func (d *Decider) Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
	d.logger.Info("Deciding loan", "taskID", task.ID, "businessKey", task.BusinessKey)

	if task.BusinessKey == "" || d.store == nil {
		d.logger.Info("No businessKey or store; defaulting to reject", "taskID", task.ID)
		if err := complete().StringVariable("decision", "reject").Execute(); err != nil {
			return err
		}
		return nil
	}

	// Read raw Scores slice from the in-memory store; demo code assumes Scores is used.
	scores, ok := d.store.GetScores(task.BusinessKey)
	if !ok || len(scores) == 0 {
		d.logger.Info("No scores recorded; storing rejection result", "businessKey", task.BusinessKey)
		// Store a rejection result when no scores are available
		res := storepkg.Result{
			Score:          0,
			LoanGranted:    false,
			ApprovedAmount: 0,
			InterestRate:   0,
			Message:        "No credit scores available; application rejected",
		}
		d.store.AppendResult(task.BusinessKey, res)

		if err := complete().Execute(); err != nil {
			return fmt.Errorf("failed to complete decider task: %w", err)
		}
		return nil
	}

	total := 0
	for _, s := range scores {
		total += s
	}
	avg := float64(total) / float64(len(scores))

	// Build and store final Result directly in the external store (no process variables)
	scoreInt := int(avg + 0.5)

	// Fetch application details for message/amount
	app, _ := d.store.Get(task.BusinessKey)
	requestedAmount := app.RequestedAmount

	if avg > 5.0 {
		// Grant loan
		approvalPercentage := (float64(scoreInt) / 10.0) * 100.0
		approvedAmount := requestedAmount * (approvalPercentage / 100.0)
		interestRate := 15.0 - (float64(scoreInt) - 5.0)
		if interestRate < 5.0 {
			interestRate = 5.0
		}
		res := storepkg.Result{
			Score:          scoreInt,
			LoanGranted:    true,
			ApprovedAmount: approvedAmount,
			InterestRate:   interestRate,
			Message:        fmt.Sprintf("Congratulations! Your loan of $%.2f has been approved at %.2f%% interest rate.", approvedAmount, interestRate),
		}
		d.store.AppendResult(task.BusinessKey, res)
		d.logger.Info("Loan granted stored in in-memory DB", "businessKey", task.BusinessKey, "approvedAmount", res.ApprovedAmount)
	} else {
		// Reject loan
		reason := fmt.Sprintf("Average score %.2f does not meet minimum requirement", avg)
		res := storepkg.Result{
			Score:          scoreInt,
			LoanGranted:    false,
			ApprovedAmount: 0,
			InterestRate:   0,
			Message:        fmt.Sprintf("Rejection: %s", reason),
		}
		d.store.AppendResult(task.BusinessKey, res)
		d.logger.Info("Loan rejected stored in in-memory DB", "businessKey", task.BusinessKey, "reason", reason)
	}

	if err := complete().Execute(); err != nil {
		return fmt.Errorf("failed to complete decider task: %w", err)
	}

	return nil
}
