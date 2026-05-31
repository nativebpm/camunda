package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nativebpm/camunda"
)

//go:embed bpmn/*
var bpmnFS embed.FS

//go:embed web/*
var webFS embed.FS

var (
	camundaURL = "http://localhost:8080"
	serverPort = "8081"
	client     *camunda.Client
	logger     *slog.Logger
)

func main() {
	// Initialize logger
	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Read environment configuration
	if url := os.Getenv("CAMUNDA_URL"); url != "" {
		camundaURL = url
	}
	if port := os.Getenv("PORT"); port != "" {
		serverPort = port
	}

	logger.Info("Starting BPMN 2.0 Showcase", "camundaURL", camundaURL, "port", serverPort)

	// Create Camunda client
	var err error
	client, err = camunda.NewClient(camundaURL, "bpmn-spec-worker")
	if err != nil {
		logger.Error("Failed to create Camunda client", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Auto-deploy BPMN and DMN resources
	if err := deployResources(ctx); err != nil {
		logger.Error("Failed to deploy resources", "error", err)
		// We log the error but don't exit, in case Camunda is temporarily down or we want to retry later.
	}

	// 2. Initialize and start external task workers
	worker := camunda.NewWorker(client, logger)
	registerWorkerHandlers(worker)

	go func() {
		logger.Info("Starting external task workers...")
		worker.Start(ctx)
	}()

	// 3. Set up HTTP router
	mux := http.NewServeMux()

	// REST API Endpoints
	mux.HandleFunc("GET /api/processes", handleGetProcesses)
	mux.HandleFunc("GET /api/processes/{key}/xml", handleGetProcessXML)
	mux.HandleFunc("POST /api/processes/{key}/start", handleStartProcess)
	mux.HandleFunc("GET /api/instances/{id}/active-activities", handleGetActiveActivities)
	mux.HandleFunc("GET /api/instances/{id}/variables", handleGetVariables)
	mux.HandleFunc("GET /api/instances/{id}/tasks", handleGetTasks)
	mux.HandleFunc("POST /api/instances/{id}/trigger-event", handleTriggerEvent)
	mux.HandleFunc("POST /api/tasks/{id}/complete", handleCompleteTask)

	// Embedded Web GUI Static Files
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		logger.Error("Failed to sub web FS", "error", err)
		os.Exit(1)
	}
	mux.Handle("/", http.FileServer(http.FS(webSub)))

	// Start server
	server := &http.Server{
		Addr:    ":" + serverPort,
		Handler: mux,
	}

	go func() {
		logger.Info("Server listening", "url", "http://localhost:"+serverPort)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Server listen failed", "error", err)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down gracefully...")
	cancel() // stops workers

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown failed", "error", err)
	}

	logger.Info("Showcase stopped.")
}

// deployResources scans embedded bpmn/ folder and uploads to Camunda
func deployResources(ctx context.Context) error {
	entries, err := bpmnFS.ReadDir("bpmn")
	if err != nil {
		return fmt.Errorf("failed to read embedded bpmn directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".bpmn") && !strings.HasSuffix(name, ".dmn") {
			continue
		}

		file, err := bpmnFS.Open("bpmn/" + name)
		if err != nil {
			return fmt.Errorf("failed to open embedded file %s: %w", name, err)
		}

		logger.Info("Deploying resource to Camunda", "file", name)
		depID, err := client.DeployProcess(ctx, "bpmn-spec-"+name, file, name)
		file.Close()
		if err != nil {
			return fmt.Errorf("failed to deploy resource %s: %w", name, err)
		}
		logger.Info("Resource deployed successfully", "file", name, "deploymentID", depID)
	}
	return nil
}

// registerWorkerHandlers defines logic for all BPMN service tasks
func registerWorkerHandlers(w *camunda.Worker) {
	// Gateways Process workers
	w.RegisterHandler("init-data", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		// Try to read input score or fallback to random
		score := 60
		if val, ok := task.Variables["inputScore"]; ok && val.Value != nil {
			if floatVal, ok := val.Value.(float64); ok {
				score = int(floatVal)
			}
		}
		logger.Info("Worker init-data executing", "score", score)
		return complete().IntVariable("score", score).Execute()
	}), 30000, nil)

	w.RegisterHandler("high-score", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker high-score executing")
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("low-score", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker low-score executing")
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("background-check", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker background-check starting (sleeping 2s)...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		logger.Info("Worker background-check completed")
		return complete().Execute()
	}), 30000, nil)

	// Events Process workers
	w.RegisterHandler("escalate-timeout", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker escalate-timeout executing")
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("cancel-cleanup", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker cancel-cleanup executing")
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("process-payment", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker process-payment starting (sleeping 2s)...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}

		// Read variable to decide if it should fail
		failPayment := false
		if val, ok := task.Variables["failPayment"]; ok && val.Value != nil {
			if b, ok := val.Value.(bool); ok {
				failPayment = b
			}
		}

		if failPayment {
			logger.Warn("Worker process-payment: simulated failure! Throwing BPMN Error PAYMENT_FAILED")
			return c.BpmnError(task.ID, "PAYMENT_FAILED", "Insufficient funds on card").Execute()
		}

		logger.Info("Worker process-payment completed successfully")
		return complete().Execute()
	}), 30000, []string{"failPayment"})

	w.RegisterHandler("handle-payment-failure", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker handle-payment-failure executing")
		return complete().Execute()
	}), 30000, nil)

	// Subprocesses Process workers
	w.RegisterHandler("init-order", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker init-order executing")
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("pack-items", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker pack-items starting (sleeping 1s)...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
		logger.Info("Worker pack-items completed")
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("ship-items", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker ship-items starting (sleeping 1s)...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}

		failShipping := false
		if val, ok := task.Variables["failShipping"]; ok && val.Value != nil {
			if b, ok := val.Value.(bool); ok {
				failShipping = b
			}
		}

		if failShipping {
			logger.Warn("Worker ship-items: simulated shipping failure! Throwing BPMN Error SHIPPING_FAILED")
			return c.BpmnError(task.ID, "SHIPPING_FAILED", "Courier vehicle broke down").Execute()
		}

		logger.Info("Worker ship-items completed successfully")
		return complete().Execute()
	}), 30000, []string{"failShipping"})

	w.RegisterHandler("refund-customer", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker refund-customer executing")
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("release-stock", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		logger.Info("Worker release-stock executing")
		return complete().Execute()
	}), 30000, nil)

	// DMN Process workers
	w.RegisterHandler("apply-discount", camunda.TaskHandlerFunc(func(ctx context.Context, c *camunda.Client, task camunda.ExternalTask, complete camunda.CompleteFunc, fail camunda.FailFunc) error {
		discount := 0
		if val, ok := task.Variables["discount"]; ok && val.Value != nil {
			if floatVal, ok := val.Value.(float64); ok {
				discount = int(floatVal)
			}
		}
		logger.Info("Worker apply-discount executing", "resolvedDiscountPercentage", discount)
		return complete().Execute()
	}), 30000, []string{"discount"})
}

// ProcessInfo represents metadata of showcased processes
type ProcessInfo struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

var processes = []ProcessInfo{
	{
		Key:         "gateways_process",
		Name:        "Gateways (Exclusive & Parallel)",
		Description: "Demonstrates Exclusive Gateway condition routing and Parallel Gateway concurrent execution paths with user approvals.",
	},
	{
		Key:         "events_process",
		Name:        "Events (Timer, Message & Error)",
		Description: "Demonstrates boundary events including timer escalations, asynchronous message cancellation, and business error boundary mapping.",
	},
	{
		Key:         "subprocesses_process",
		Name:        "Subprocesses & Event Subprocess",
		Description: "Demonstrates Embedded Subprocess scoping, local boundary handlers, and global Event Subprocesses that interrupt runtime state on message events.",
	},
	{
		Key:         "dmn_process",
		Name:        "DMN Decision Tables",
		Description: "Integrates Business Rule Task with a Decision Table (determine_discount) in DMN 1.3 to resolve variables on execution.",
	},
}

func handleGetProcesses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(processes)
}

func handleGetProcessXML(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var filename string
	switch key {
	case "gateways_process":
		filename = "gateways.bpmn"
	case "events_process":
		filename = "events.bpmn"
	case "subprocesses_process":
		filename = "subprocesses.bpmn"
	case "dmn_process":
		filename = "dmn_business_rule.bpmn"
	default:
		http.Error(w, "Process key not found", http.StatusNotFound)
		return
	}

	data, err := bpmnFS.ReadFile("bpmn/" + filename)
	if err != nil {
		logger.Error("Failed to read XML", "file", filename, "error", err)
		http.Error(w, "Failed to read XML", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.Write(data)
}

func handleStartProcess(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var reqVariables map[string]any
	if err := json.NewDecoder(r.Body).Decode(&reqVariables); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Adapt variables to Camunda format
	variables := make(map[string]camunda.Variable)
	for k, v := range reqVariables {
		switch val := v.(type) {
		case string:
			variables[k] = camunda.StringVariable(val)
		case bool:
			variables[k] = camunda.BooleanVariable(val)
		case float64:
			// JSON numbers are parsed as float64, check if it's an integer
			if val == float64(int(val)) {
				variables[k] = camunda.IntVariable(int(val))
			} else {
				variables[k] = camunda.DoubleVariable(val)
			}
		}
	}

	businessKey := "bk-" + uuid.New().String()[:8]
	logger.Info("Starting process instance", "key", key, "businessKey", businessKey, "variables", reqVariables)

	instID, err := client.StartProcessInstance(r.Context(), key, businessKey, variables)
	if err != nil {
		logger.Error("Failed to start process", "key", key, "error", err)
		http.Error(w, "Failed to start process: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"processInstanceId": instID,
		"businessKey":       businessKey,
	})
}

func handleGetVariables(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vars, err := client.GetProcessVariables(r.Context(), id)
	if err != nil {
		logger.Error("Failed to get process variables", "id", id, "error", err)
		http.Error(w, "Failed to get process variables: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vars)
}

// ActivityInstance node representation
type ActivityInstance struct {
	ID                     string             `json:"id"`
	ActivityID             string             `json:"activityId"`
	ActivityName           string             `json:"activityName"`
	ActivityType           string             `json:"activityType"`
	ChildActivityInstances []ActivityInstance `json:"childActivityInstances"`
}

func handleGetActiveActivities(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Call Camunda REST API: GET /process-instance/{id}/activity-instances
	reqURL := fmt.Sprintf("%s/engine-rest/process-instance/%s/activity-instances", camundaURL, id)
	req, err := http.NewRequestWithContext(r.Context(), "GET", reqURL, nil)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error("Failed to get activity instances", "id", id, "error", err)
		http.Error(w, "Failed to connect to Camunda", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("Camunda error %d: %s", resp.StatusCode, string(body)), resp.StatusCode)
		return
	}

	var root ActivityInstance
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		http.Error(w, "Failed to decode activity instances", http.StatusInternalServerError)
		return
	}

	activeIDs := collectActiveActivities(root)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(activeIDs)
}

func collectActiveActivities(node ActivityInstance) []string {
	var ids []string
	if node.ActivityType != "processDefinition" && node.ActivityID != "" {
		ids = append(ids, node.ActivityID)
	}
	for _, child := range node.ChildActivityInstances {
		ids = append(ids, collectActiveActivities(child)...)
	}
	return ids
}

type UserTaskInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	TaskDefKey     string `json:"taskDefinitionKey"`
	Assignee       string `json:"assignee"`
	CreateTime     string `json:"created"`
	ProcessInstID  string `json:"processInstanceId"`
}

func handleGetTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Call Camunda: GET /task?processInstanceId={id}
	reqURL := fmt.Sprintf("%s/engine-rest/task?processInstanceId=%s", camundaURL, id)
	req, err := http.NewRequestWithContext(r.Context(), "GET", reqURL, nil)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error("Failed to get tasks", "instanceID", id, "error", err)
		http.Error(w, "Failed to connect to Camunda", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("Camunda error %d: %s", resp.StatusCode, string(body)), resp.StatusCode)
		return
	}

	var tasks []UserTaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		http.Error(w, "Failed to decode tasks", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var reqVariables map[string]any
	if err := json.NewDecoder(r.Body).Decode(&reqVariables); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Format variables for Camunda REST API format:
	// {"variables": {"varName": {"value": val, "type": "String/Boolean/Integer"}}}
	camVars := make(map[string]map[string]any)
	for k, v := range reqVariables {
		varType := "String"
		varVal := v
		switch val := v.(type) {
		case bool:
			varType = "Boolean"
		case float64:
			if val == float64(int(val)) {
				varType = "Integer"
				varVal = int(val)
			} else {
				varType = "Double"
			}
		}
		camVars[k] = map[string]any{
			"value": varVal,
			"type":  varType,
		}
	}

	payload := map[string]any{
		"variables": camVars,
	}

	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/engine-rest/task/%s/complete", camundaURL, taskID)
	req, err := http.NewRequestWithContext(r.Context(), "POST", reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error("Failed to complete task", "taskID", taskID, "error", err)
		http.Error(w, "Failed to connect to Camunda", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("Camunda error %d: %s", resp.StatusCode, string(body)), resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleTriggerEvent(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("id")
	var req struct {
		EventName string `json:"eventName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.EventName == "" {
		http.Error(w, "eventName is required", http.StatusBadRequest)
		return
	}

	logger.Info("Triggering message correlation", "instanceID", instanceID, "messageName", req.EventName)

	// Call Camunda REST API: POST /message
	// Payload: {"messageName": req.EventName, "processInstanceId": instanceID}
	payload := map[string]any{
		"messageName":       req.EventName,
		"processInstanceId": instanceID,
	}
	payloadBytes, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s/engine-rest/message", camundaURL)
	reqMsg, err := http.NewRequestWithContext(r.Context(), "POST", reqURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}
	reqMsg.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(reqMsg)
	if err != nil {
		logger.Error("Failed to correlate message", "messageName", req.EventName, "error", err)
		http.Error(w, "Failed to connect to Camunda", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		// If message correlation fails, try sending a signal as fallback
		logger.Info("Message correlation failed, trying signal delivery", "status", resp.StatusCode)
		
		signalPayload := map[string]any{
			"name":      req.EventName,
			"variables": map[string]any{},
		}
		signalBytes, _ := json.Marshal(signalPayload)
		reqSigURL := fmt.Sprintf("%s/engine-rest/signal", camundaURL)
		reqSig, err := http.NewRequestWithContext(r.Context(), "POST", reqSigURL, strings.NewReader(string(signalBytes)))
		if err == nil {
			reqSig.Header.Set("Content-Type", "application/json")
			respSig, errSig := http.DefaultClient.Do(reqSig)
			if errSig == nil {
				defer respSig.Body.Close()
				if respSig.StatusCode == http.StatusNoContent || respSig.StatusCode == http.StatusOK {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}

		http.Error(w, fmt.Sprintf("Event trigger failed. Camunda response: %s", string(body)), resp.StatusCode)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
