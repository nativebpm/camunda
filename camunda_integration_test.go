package camunda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nativebpm/bpmn"
)

const integrationBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" xmlns:di="http://www.omg.org/spec/DD/20100524/DI" id="Definitions_1" targetNamespace="http://bpmn.io/schema/bpmn" exporter="Camunda Modeler" exporterVersion="5.0.0">
  <bpmn:process id="IntegrationTestProcess" name="Integration Test Process" isExecutable="true" camunda:historyTimeToLive="180">
    <bpmn:startEvent id="StartEvent_1" name="Start">
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_WasmTask" />
    <bpmn:serviceTask id="Activity_WasmTask" name="Integration Task" camunda:type="external" camunda:topic="integration-test-topic">
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_WasmTask" targetRef="EndEvent_1" />
    <bpmn:endEvent id="EndEvent_1" name="End">
      <bpmn:incoming>Flow_2</bpmn:incoming>
    </bpmn:endEvent>
  </bpmn:process>
</bpmn:definitions>`

func TestCamundaIntegration_RealServer(t *testing.T) {
	// 1. Check if local Camunda Server is running
	clientHTTP := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := clientHTTP.Get("http://localhost:8080/engine-rest/version")
	if err != nil {
		t.Skip("Skipping integration test: Camunda Server is not running on http://localhost:8080/engine-rest")
		return
	}
	resp.Body.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 2. Initialize Client
	client, err := NewClient("http://localhost:8080", "integration-test-worker")
	require.NoError(t, err)

	// 3. Deploy Process definition to Camunda
	deployID, err := client.DeployProcess(ctx, "integration-deployment", strings.NewReader(integrationBPMN), "integration.bpmn")
	require.NoError(t, err)
	defer func() {
		// Cleanup deployment
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_ = client.DeleteDeployment(cleanupCtx, deployID, true)
	}()

	// 4. Start Process Instance
	businessKey := "biz-integration-" + time.Now().Format("20060102-150405")
	processInstanceID, err := client.StartProcessInstance(ctx, "IntegrationTestProcess", businessKey, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, processInstanceID)

	// 5. Setup Worker to poll and complete the external task
	w := NewWorker(client, nil)
	w.SetMaxTasks(1)
	w.SetPollInterval(100 * time.Millisecond)

	taskProcessedChan := make(chan bool, 1)

	w.RegisterHandler("integration-test-topic", TaskHandlerFunc(func(ctx context.Context, c *Client, task ExternalTask, complete CompleteFunc, fail FailFunc) error {
		if task.BusinessKey == businessKey {
			// Complete task in Camunda
			errComplete := complete().Execute()
			if errComplete != nil {
				return errComplete
			}
			taskProcessedChan <- true
		}
		return nil
	}), 60000, []string{})

	// Start worker in background
	go w.Start(ctx)

	// 6. Wait for worker to poll and successfully complete the task
	select {
	case processed := <-taskProcessedChan:
		assert.True(t, processed, "Task must be processed successfully by the worker")
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for Camunda worker to process task")
	}
}

type UserTaskInfo struct {
	ID         string `json:"id"`
	TaskDefKey string `json:"taskDefinitionKey"`
}

func TestBPMNVSRealCamunda(t *testing.T) {
	// 1. Check if local Camunda Server is running
	clientHTTP := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := clientHTTP.Get("http://localhost:8080/engine-rest/version")
	if err != nil {
		t.Skip("Skipping comparison test: Camunda Server is not running on http://localhost:8080/engine-rest")
		return
	}
	resp.Body.Close()

	// 2. Read gateways.bpmn XML schema
	xmlData, err := os.ReadFile("examples/bpmn-spec/bpmn/gateways.bpmn")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 3. Initialize Camunda Client
	cClient, err := NewClient("http://localhost:8080", "comparison-test-worker")
	require.NoError(t, err)

	// 4. Deploy schema to Camunda
	deployID, err := cClient.DeployProcess(ctx, "compare-deployment", strings.NewReader(string(xmlData)), "gateways.bpmn")
	require.NoError(t, err)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_ = cClient.DeleteDeployment(cleanupCtx, deployID, true)
	}()

	// 5. Setup Workers for ServiceTasks in Camunda
	w := NewWorker(cClient, nil)
	w.SetMaxTasks(5)
	w.SetPollInterval(50 * time.Millisecond)

	w.RegisterHandler("init-data", TaskHandlerFunc(func(ctx context.Context, c *Client, task ExternalTask, complete CompleteFunc, fail FailFunc) error {
		return complete().IntVariable("score", 60).Execute()
	}), 30000, nil)

	w.RegisterHandler("high-score", TaskHandlerFunc(func(ctx context.Context, c *Client, task ExternalTask, complete CompleteFunc, fail FailFunc) error {
		return complete().Execute()
	}), 30000, nil)

	w.RegisterHandler("background-check", TaskHandlerFunc(func(ctx context.Context, c *Client, task ExternalTask, complete CompleteFunc, fail FailFunc) error {
		return complete().Execute()
	}), 30000, nil)

	go w.Start(ctx)

	// 6. Start instance in Camunda
	businessKey := "bk-compare-" + time.Now().Format("20060102-150405")
	vars := map[string]Variable{
		"inputScore": IntVariable(60),
	}
	processInstanceID, err := cClient.StartProcessInstance(ctx, "gateways_process", businessKey, vars)
	require.NoError(t, err)

	// 7. Wait for process to reach UserTask: Activity_User_Approve
	var tasks []UserTaskInfo
	for i := 0; i < 40; i++ {
		reqURL := fmt.Sprintf("http://localhost:8080/engine-rest/task?processInstanceId=%s", processInstanceID)
		req, _ := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		respMsg, err := http.DefaultClient.Do(req)
		if err == nil && respMsg.StatusCode == http.StatusOK {
			var tList []UserTaskInfo
			if json.NewDecoder(respMsg.Body).Decode(&tList) == nil && len(tList) > 0 {
				tasks = tList
				respMsg.Body.Close()
				break
			}
		}
		if respMsg != nil {
			respMsg.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEmpty(t, tasks, "User task should appear in Camunda")
	assert.Equal(t, "Activity_User_Approve", tasks[0].TaskDefKey)

	// 8. Complete UserTask in Camunda
	taskID := tasks[0].ID
	reqURL := fmt.Sprintf("http://localhost:8080/engine-rest/task/%s/complete", taskID)
	reqMsg, _ := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(`{"variables": {}}`))
	reqMsg.Header.Set("Content-Type", "application/json")
	respComplete, err := http.DefaultClient.Do(reqMsg)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, respComplete.StatusCode)
	respComplete.Body.Close()

	// 9. Wait for process instance completion in Camunda
	completedInCamunda := false
	for i := 0; i < 20; i++ {
		reqURLInst := fmt.Sprintf("http://localhost:8080/engine-rest/process-instance/%s", processInstanceID)
		reqInst, _ := http.NewRequestWithContext(ctx, "GET", reqURLInst, nil)
		respInst, err := http.DefaultClient.Do(reqInst)
		if err == nil && respInst.StatusCode == http.StatusNotFound {
			completedInCamunda = true
			respInst.Body.Close()
			break
		}
		if respInst != nil {
			respInst.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.True(t, completedInCamunda, "Process instance should complete in Camunda")

	// ==========================================
	// RUNNING ON OUR OWN BPMN ENGINE
	// ==========================================
	pp, err := bpmn.ParseBPMN(xmlData)
	require.NoError(t, err)
	assert.Equal(t, "gateways_process", pp.ID)

	engine := bpmn.NewEngine(pp, nil)
	instance, err := engine.StartInstance("instance-compare", map[string]interface{}{
		"score": 60, // Предоставляем результат Activity_Init шага
	})
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "StartEvent_1")

	// Step 1: StartEvent_1 -> Activity_Init
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Activity_Init")

	// Step 2: Activity_Init -> Gateway_Exclusive
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Gateway_Exclusive")

	// Step 3: Gateway_Exclusive -> Activity_High_Score (since score = 60 > 50)
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Activity_High_Score")

	// Step 4: Activity_High_Score -> Gateway_Join_Exclusive
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Gateway_Join_Exclusive")

	// Step 5: Gateway_Join_Exclusive -> Gateway_Parallel
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Gateway_Parallel")

	// Step 6: Gateway_Parallel -> [Activity_User_Approve, Activity_Background_Check]
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Activity_User_Approve")
	assert.Contains(t, instance.ActiveTokens, "Activity_Background_Check")

	// Step 7: Process Activity_User_Approve (UserTask) -> moves it to WaitingTokens
	// ActiveTokens remaining: [Activity_Background_Check], WaitingTokens: [Activity_User_Approve]
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Activity_Background_Check")
	assert.Contains(t, instance.WaitingTokens, "Activity_User_Approve")

	// Step 8: Process Activity_Background_Check (ServiceTask) -> moves it to Gateway_Join_Parallel
	// ActiveTokens: [Gateway_Join_Parallel], WaitingTokens: [Activity_User_Approve]
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Gateway_Join_Parallel")
	assert.Contains(t, instance.WaitingTokens, "Activity_User_Approve")

	// Step 9: Process Gateway_Join_Parallel (AND join gateway).
	// Since Activity_User_Approve has NOT arrived yet, token is parked at Gateway_Join_Parallel.
	// ActiveTokens becomes: [Gateway_Join_Parallel], WaitingTokens: [Activity_User_Approve]
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "Gateway_Join_Parallel")
	assert.Contains(t, instance.WaitingTokens, "Activity_User_Approve")

	// Step 10: Complete UserTask in our engine
	err = engine.CompleteTask(instance, "Activity_User_Approve", nil)
	require.NoError(t, err)
	// ActiveTokens: [Gateway_Join_Parallel, Gateway_Join_Parallel], WaitingTokens: []
	assert.Contains(t, instance.ActiveTokens, "Gateway_Join_Parallel")
	assert.Empty(t, instance.WaitingTokens)

	// Step 11: Process Gateway_Join_Parallel -> satisfied -> moves to EndEvent_1
	// ActiveTokens: [EndEvent_1]
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Contains(t, instance.ActiveTokens, "EndEvent_1")

	// Step 12: Process EndEvent_1 -> Completed!
	err = engine.Step(ctx, instance)
	require.NoError(t, err)
	assert.Empty(t, instance.ActiveTokens)
	assert.True(t, instance.Completed)
}
