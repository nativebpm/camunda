package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nativebpm/camunda"
)

// In-memory atomic counters for stats tracking
var (
	startedInstances  atomic.Int64
	completedChecker  atomic.Int64
	completedDecision atomic.Int64 // tracks loanGranter + requestRejecter completes
	failedTasks       atomic.Int64
)

func main() {
	// Use slog for all logs as requested
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo, // Set to Info so we can see configuration and results
	}))

	// Parameters
	concurrency := 20
	if val := os.Getenv("LOAD_CONCURRENCY"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			concurrency = parsed
		}
	}

	totalProcesses := 100
	if val := os.Getenv("LOAD_PROCESSES_COUNT"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			totalProcesses = parsed
		}
	}

	submissionDelayMs := 0
	if val := os.Getenv("LOAD_SUBMISSION_DELAY_MS"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed >= 0 {
			submissionDelayMs = parsed
		}
	}

	logger.Info("CAMUNDA LOAD TEST INITIALIZED",
		"concurrency", concurrency,
		"total_processes", totalProcesses,
		"submission_delay_ms", submissionDelayMs,
		"target_checker_tasks", totalProcesses,
		"target_decision_tasks", totalProcesses*3,
		"target_total_tasks", totalProcesses*4,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := camunda.NewClient("http://localhost:8080", "loadtest-worker")
	if err != nil {
		logger.Error("Failed to create client", "error", err)
		return
	}

	// Deploy the BPMN process
	deploymentID, err := deployProcess(ctx, client, logger)
	if err != nil {
		logger.Error("Failed to deploy BPMN process", "error", err)
		return
	}

	// We expect 3 decision completions per process instance (multi-instance collection of size 3)
	expectedDecisions := int64(totalProcesses * 3)
	doneChan := make(chan struct{})

	// Spin up a monitor goroutine to check progress
	startTime := time.Now()
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-doneChan:
				return
			case <-ticker.C:
				elapsed := time.Since(startTime).Seconds()
				dec := completedDecision.Load()
				chk := completedChecker.Load()
				started := startedInstances.Load()
				logger.Info("Load test progress status",
					"elapsed_seconds", fmt.Sprintf("%.1f", elapsed),
					"started_instances", started,
					"checker_completed", chk,
					"decision_completed", dec,
					"expected_decisions", expectedDecisions,
					"percentage", fmt.Sprintf("%.1f%%", float64(dec)/float64(expectedDecisions)*100),
				)
			}
		}
	}()

	// Configure workers
	w := camunda.NewWorker(client, logger)
	w.SetMaxTasks(concurrency)
	w.SetMaxConcurrency(concurrency * 2)
	w.SetPollInterval(50 * time.Millisecond) // Poll aggressively for load testing
	w.SetAsyncResponseTimeout(5 * time.Second)

	// Register workers
	// Mock credit score checker - sets 3 scores in list (collections)
	w.RegisterHandler("creditScoreChecker", camunda.TaskHandlerFunc(func(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		scores := []int{6, 7, 5}
		err := complete().ListVariable("creditScores", scores).Execute()
		if err != nil {
			failedTasks.Add(1)
			return err
		}
		completedChecker.Add(1)
		return nil
	}), 60000, []string{})

	// Mock loan granter
	w.RegisterHandler("loanGranter", camunda.TaskHandlerFunc(func(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		err := complete().Execute()
		if err != nil {
			failedTasks.Add(1)
			return err
		}
		decVal := completedDecision.Add(1)
		if decVal >= expectedDecisions {
			select {
			case <-doneChan:
			default:
				close(doneChan)
			}
		}
		return nil
	}), 60000, []string{"score"})

	// Mock request rejecter
	w.RegisterHandler("requestRejecter", camunda.TaskHandlerFunc(func(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		err := complete().Execute()
		if err != nil {
			failedTasks.Add(1)
			return err
		}
		decVal := completedDecision.Add(1)
		if decVal >= expectedDecisions {
			select {
			case <-doneChan:
			default:
				close(doneChan)
			}
		}
		return nil
	}), 60000, []string{"score"})

	// Start worker in background
	go w.Start(ctx)

	// Start submitting instances concurrently
	submitStart := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20) // Limit submission concurrency

	for i := 1; i <= totalProcesses; i++ {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			businessKey := "loadtest-" + uuid.NewString()
			varsMap := map[string]camunda.Variable{
				"applicantName":   camunda.StringVariable(fmt.Sprintf("Load %d", num)),
				"requestedAmount": camunda.DoubleVariable(5000.0),
				"employmentYears": camunda.IntVariable(rand.Intn(10)),
			}

			_, err := client.StartProcessInstance(ctx, "loan_process", businessKey, varsMap)
			if err != nil {
				failedTasks.Add(1)
				return
			}
			startedInstances.Add(1)

			if submissionDelayMs > 0 {
				time.Sleep(time.Duration(submissionDelayMs) * time.Millisecond)
			}
		}(i)
	}

	// Wait for all submissions to finish
	wg.Wait()
	submitDuration := time.Since(submitStart)
	logger.Info("Finished submitting instances to Camunda Engine",
		"count", totalProcesses,
		"duration", submitDuration,
		"avg_ms_per_submit", float64(submitDuration.Milliseconds())/float64(totalProcesses),
	)

	// Wait for all decision tasks to finish processing
	<-doneChan
	totalDuration := time.Since(startTime)

	// Clean up deployment at the end to keep the db stateless
	_ = client.DeleteDeployment(context.Background(), deploymentID, true)
	logger.Info("Cleaned up deployment successfully", "deployment_id", deploymentID)

	// Print Summary using structured slog logging
	logger.Info("CAMUNDA LOAD TEST RESULTS",
		"total_duration", totalDuration,
		"instances_submitted", startedInstances.Load(),
		"checker_completed", completedChecker.Load(),
		"decisions_completed", completedDecision.Load(),
		"total_tasks_handled", completedChecker.Load()+completedDecision.Load(),
		"failed_tasks", failedTasks.Load(),
		"throughput_rps", fmt.Sprintf("%.2f", float64(totalProcesses)/totalDuration.Seconds()),
		"task_throughput_tps", fmt.Sprintf("%.2f", float64(completedChecker.Load()+completedDecision.Load())/totalDuration.Seconds()),
	)
}

func deployProcess(ctx context.Context, client *camunda.Client, logger *slog.Logger) (string, error) {
	// Read BPMN file from loan-granting example
	file, err := os.Open("../loan-granting/bpmn/loan-granting.bpmn")
	if err != nil {
		return "", err
	}
	defer file.Close()

	deploymentID, err := client.DeployProcess(ctx, "loadtest-deployment", file, "loan-granting.bpmn")
	if err != nil {
		return "", err
	}
	logger.Info("Deployed process for load testing", "deployment_id", deploymentID)
	return deploymentID, nil
}
