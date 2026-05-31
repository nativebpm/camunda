package main

import (
	"context"
	"encoding/json"
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
	// Disable verbose debug logging for load testing to reduce console I/O bottleneck
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Only log warnings or errors during load test
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

	fmt.Printf("==================================================\n")
	fmt.Printf("  CAMUNDA LOAD TEST CONFIGURATION\n")
	fmt.Printf("==================================================\n")
	fmt.Printf("  Concurrency (Max Tasks): %d\n", concurrency)
	fmt.Printf("  Total Process Instances: %d\n", totalProcesses)
	fmt.Printf("  Submission Delay:        %d ms\n", submissionDelayMs)
	fmt.Printf("  Target Tasks to Process: %d (%d checker, %d decisions)\n",
		totalProcesses*4, totalProcesses, totalProcesses*3)
	fmt.Printf("==================================================\n\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := camunda.NewClient("http://localhost:8080", "loadtest-worker")
	if err != nil {
		fmt.Printf("Failed to create client: %v\n", err)
		return
	}

	// Deploy the BPMN process
	deploymentID, err := deployProcess(ctx, client)
	if err != nil {
		fmt.Printf("Failed to deploy BPMN process: %v\n", err)
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
				fmt.Printf("[%.1fs elapsed] Started: %d/%d | Checker Completed: %d/%d | Decision Completed: %d/%d (%.1f%%)\n",
					elapsed, started, totalProcesses, chk, totalProcesses, dec, expectedDecisions, float64(dec)/float64(expectedDecisions)*100)
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
			// Start instance with some simple variables to simulate load
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
	fmt.Printf("Finished submitting %d instances in %v (avg %.2f ms/submit)\n",
		totalProcesses, submitDuration, float64(submitDuration.Milliseconds())/float64(totalProcesses))

	// Wait for all decision tasks to finish processing
	<-doneChan
	totalDuration := time.Since(startTime)

	// Clean up deployment at the end to keep the db stateless
	_ = client.DeleteDeployment(context.Background(), deploymentID, true)
	fmt.Printf("Cleaned up deployment: %s\n", deploymentID)

	// Print Summary
	fmt.Printf("\n==================================================\n")
	fmt.Printf("  LOAD TEST RESULTS\n")
	fmt.Printf("==================================================\n")
	fmt.Printf("  Total Duration:         %v\n", totalDuration)
	fmt.Printf("  Instances Submitted:    %d\n", startedInstances.Load())
	fmt.Printf("  Checker Completed:      %d\n", completedChecker.Load())
	fmt.Printf("  Decisions Completed:    %d\n", completedDecision.Load())
	fmt.Printf("  Total Tasks Handled:    %d\n", completedChecker.Load()+completedDecision.Load())
	fmt.Printf("  Failed Task Requests:   %d\n", failedTasks.Load())
	fmt.Printf("  Throughput (RPS):       %.2f instances/sec\n", float64(totalProcesses)/totalDuration.Seconds())
	fmt.Printf("  Task Throughput (TPS):  %.2f tasks/sec\n", float64(completedChecker.Load()+completedDecision.Load())/totalDuration.Seconds())
	fmt.Printf("==================================================\n")
}

func deployProcess(ctx context.Context, client *camunda.Client) (string, error) {
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
	fmt.Printf("Deployed process for load testing. Deployment ID: %s\n", deploymentID)
	return deploymentID, nil
}

// Simple JSON helper
func importJSON(data []byte, val any) {
	_ = json.Unmarshal(data, val)
}
