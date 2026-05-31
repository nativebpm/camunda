package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nativebpm/camunda"
	storepkg "github.com/nativebpm/camunda/examples/loan-granting/store"
)

// CreditScoreChecker handles credit score checking tasks
type CreditScoreChecker struct {
	logger *slog.Logger
	store  *storepkg.Store
}

// NewCreditScoreChecker creates a new credit score checker handler
func NewCreditScoreChecker(logger *slog.Logger, store *storepkg.Store) *CreditScoreChecker {
	return &CreditScoreChecker{
		logger: logger,
		store:  store,
	}
}

// Handle processes a credit score checking task
func (h *CreditScoreChecker) Handle(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
	h.logger.Info("Checking credit scores", "taskID", task.ID, "processInstanceID", task.ProcessInstanceID)

	// Extract applicant data from process variables
	var monthlyIncome, existingDebts float64
	var employmentYears int
	// Prefer variables, but fall back to in-memory store (by businessKey) when variables are not present
	if income, ok := task.Variables["monthlyIncome"]; ok {
		if val, ok := income.Value.(float64); ok {
			monthlyIncome = val
		}
	}
	if debts, ok := task.Variables["existingDebts"]; ok {
		if val, ok := debts.Value.(float64); ok {
			existingDebts = val
		}
	}
	if years, ok := task.Variables["employmentYears"]; ok {
		if val, ok := years.Value.(float64); ok {
			employmentYears = int(val)
		}
	}

	if monthlyIncome == 0 && task.BusinessKey != "" && h.store != nil {
		if app, ok := h.store.Get(task.BusinessKey); ok {
			monthlyIncome = app.MonthlyIncome
			existingDebts = app.ExistingDebts
			employmentYears = app.EmploymentYears
		}
	}

	h.logger.Info("Applicant financial data",
		"monthlyIncome", monthlyIncome,
		"existingDebts", existingDebts,
		"employmentYears", employmentYears)

	// Simulate credit score check - in real scenario this would call external services
	// (Equifax, Experian, TransUnion)
	time.Sleep(2 * time.Second)

	// Calculate credit scores from multiple bureaus based on applicant data
	// Higher income, lower debts, more employment years = better scores
	scores := calculateCreditScores(monthlyIncome, existingDebts, employmentYears)

	h.logger.Info("Credit scores calculated", "scores", scores, "taskID", task.ID)

	// Complete the task with results.
	// Persist raw scores to the in-memory store for later inspection/aggregation.
	if task.BusinessKey != "" && h.store != nil {
		h.store.AppendScores(task.BusinessKey, scores)
	}

	err := complete().Execute()
	if err != nil {
		return err
	}

	h.logger.Info("Credit score check completed", "taskID", task.ID)
	return nil
}

// calculateCreditScores simulates credit score calculation from multiple bureaus
// In reality, this would call external credit bureau APIs
func calculateCreditScores(monthlyIncome, existingDebts float64, employmentYears int) []int {
	// Base score starts at 5
	baseScore := 5

	// Income factor: +1 point for every $2000 of monthly income
	incomeBonus := int(monthlyIncome / 2000)

	// Debt factor: -1 point for every $3000 of existing debts
	debtPenalty := int(existingDebts / 3000)

	// Employment stability: +1 point for every 2 years of employment
	employmentBonus := employmentYears / 2

	// Calculate scores from 3 credit bureaus with slight variations
	score1 := baseScore + incomeBonus - debtPenalty + employmentBonus
	score2 := baseScore + incomeBonus - debtPenalty + employmentBonus + 1 // Equifax slightly higher
	score3 := baseScore + incomeBonus - debtPenalty + employmentBonus - 1 // TransUnion slightly lower

	// Clamp scores between 1 and 10
	clamp := func(score int) int {
		if score < 1 {
			return 1
		}
		if score > 10 {
			return 10
		}
		return score
	}

	return []int{clamp(score1), clamp(score2), clamp(score3)}
}
