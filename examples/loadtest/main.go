package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sort"
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

	startTimes  sync.Map
	durationsMu sync.Mutex
	durations   []time.Duration
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

	useSequin := os.Getenv("USE_SEQUIN") == "true"
	var sequinWorker *camunda.SequinWorker
	var normalWorker *camunda.Worker

	if useSequin {
		sequinURL := os.Getenv("SEQUIN_URL")
		if sequinURL == "" {
			sequinURL = "http://localhost:7376"
		}
		sequinConsumer := os.Getenv("SEQUIN_CONSUMER")
		if sequinConsumer == "" {
			sequinConsumer = "camunda_tasks_stream"
		}

		var err error
		sequinWorker, err = camunda.NewSequinWorker(client, sequinURL, sequinConsumer, logger)
		if err != nil {
			logger.Error("Failed to create Sequin worker", "error", err)
			return
		}
		logger.Info("Using Sequin logical CDC worker")
	} else {
		// Configure normal workers
		normalWorker = camunda.NewWorker(client, logger)
		normalWorker.SetMaxTasks(concurrency)
		normalWorker.SetMaxConcurrency(concurrency * 2)
		normalWorker.SetPollInterval(50 * time.Millisecond) // Poll aggressively for load testing
		normalWorker.SetAsyncResponseTimeout(5 * time.Second)
		logger.Info("Using standard REST polling worker")
	}

	// Handlers functions
	creditScoreCheckerHandler := camunda.TaskHandlerFunc(func(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		scores := []int{6, 7, 5}
		err := complete().ListVariable("creditScores", scores).Execute()
		if err != nil {
			failedTasks.Add(1)
			return err
		}
		completedChecker.Add(1)
		return nil
	})

	loanGranterHandler := camunda.TaskHandlerFunc(func(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		err := complete().Execute()
		if err != nil {
			failedTasks.Add(1)
			return err
		}
		if startVal, ok := startTimes.Load(task.ProcessInstanceID); ok {
			if startTime, ok := startVal.(time.Time); ok {
				durationsMu.Lock()
				durations = append(durations, time.Since(startTime))
				durationsMu.Unlock()
				startTimes.Delete(task.ProcessInstanceID)
			}
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
	})

	requestRejecterHandler := camunda.TaskHandlerFunc(func(ctx context.Context, client *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		err := complete().Execute()
		if err != nil {
			failedTasks.Add(1)
			return err
		}
		if startVal, ok := startTimes.Load(task.ProcessInstanceID); ok {
			if startTime, ok := startVal.(time.Time); ok {
				durationsMu.Lock()
				durations = append(durations, time.Since(startTime))
				durationsMu.Unlock()
				startTimes.Delete(task.ProcessInstanceID)
			}
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
	})

	if useSequin {
		sequinWorker.RegisterHandler("creditScoreChecker", creditScoreCheckerHandler)
		sequinWorker.RegisterHandler("loanGranter", loanGranterHandler)
		sequinWorker.RegisterHandler("requestRejecter", requestRejecterHandler)
		go sequinWorker.Start(ctx)
	} else {
		normalWorker.RegisterHandler("creditScoreChecker", creditScoreCheckerHandler, 60000, []string{})
		normalWorker.RegisterHandler("loanGranter", loanGranterHandler, 60000, []string{"score"})
		normalWorker.RegisterHandler("requestRejecter", requestRejecterHandler, 60000, []string{"score"})
		go normalWorker.Start(ctx)
	}

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

			instanceID, err := client.StartProcessInstance(ctx, "loan_process", businessKey, varsMap)
			if err != nil {
				failedTasks.Add(1)
				return
			}
			startTimes.Store(instanceID, time.Now())
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

	// Sort durations to calculate percentiles
	durationsMu.Lock()
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	p50 := time.Duration(0)
	p90 := time.Duration(0)
	p95 := time.Duration(0)
	p99 := time.Duration(0)
	avg := time.Duration(0)

	if len(durations) > 0 {
		var total time.Duration
		for _, d := range durations {
			total += d
		}
		avg = total / time.Duration(len(durations))

		getPercentile := func(pct float64) time.Duration {
			idx := int(float64(len(durations)) * pct / 100.0)
			if idx >= len(durations) {
				idx = len(durations) - 1
			}
			return durations[idx]
		}
		p50 = getPercentile(50)
		p90 = getPercentile(90)
		p95 = getPercentile(95)
		p99 = getPercentile(99)
	}
	durationsMu.Unlock()

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
		"p50_latency_ms", p50.Milliseconds(),
		"p90_latency_ms", p90.Milliseconds(),
		"p95_latency_ms", p95.Milliseconds(),
		"p99_latency_ms", p99.Milliseconds(),
		"avg_latency_ms", avg.Milliseconds(),
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
