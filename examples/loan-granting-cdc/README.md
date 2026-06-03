# Loan Granting CDC Worker Example (Sequin-based)

This example demonstrates how to implement Camunda External Task Workers in Go using **logical replication Change Data Capture (CDC)** with **Sequin** instead of traditional HTTP polling (`fetchAndLock`).

## REST Polling vs. Sequin CDC Worker

| Feature | Standard REST Polling Worker | Sequin CDC Worker |
| :--- | :--- | :--- |
| **Trigger Mechanism** | Polling in a loop (long polling) | Event-driven (WAL log changes captured by Sequin) |
| **API Pressure** | High `/fetchAndLock` REST request frequency | Zero idle requests to Camunda |
| **Database Overhead** | High query overhead at rest | Zero database overhead at rest |
| **Latency** | Bounded by poll interval | Immediate (sub-millisecond) response |
| **Complexity** | Extremely simple (zero infrastructure) | Requires PostgreSQL logical replication & Sequin |

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────┐
│                      Camunda Platform 7                      │
│                 (Process Engine + PostgreSQL)                │
└──────────────┬──────────────────────────────────────▲────────┘
               │ WAL Logical Replication              │
               │ (act_ru_ext_task inserts)            │ HTTP REST API
               │                                      │ (Lock, Complete)
┌──────────────▼──────────────────────────────┐       │
│                Sequin Service               │       │
│          (Logical CDC Stream HTTP)          │       │
└──────────────┬──────────────────────────────┘       │
               │ HTTP Pull Messages                   │
               │                                      │
┌──────────────▼──────────────────────────────────────┴────────┐
│                   loan-granting-cdc (Go)                     │
│  • Deploys process definitions                               │
│  • Simulates loan applications                               │
│  • SequinWorker pulls tasks from Sequin                      │
│  • Executes business logic and completes via Camunda REST   │
└──────────────────────────────────────────────────────────────┘
```

## Running the Example

### Prerequisites
- Docker (for Camunda + Sequin infrastructure)
- Go 1.21 or later

### 1. Start Camunda & Sequin
From the `camunda` module directory, start the required Docker infrastructure:
```bash
make camunda
```
This starts:
- Camunda 7 on `http://localhost:8080`
- Sequin stream service on `http://localhost:7376`
- PostgreSQL databases

### 2. Run the CDC Worker
Run the example:
```bash
cd camunda/examples/loan-granting-cdc
go run main.go
```

### Expected Output
When you run the example, you will see output like this:
```log
2026/06/03 13:55:00 INFO Deployed BPMN process deploymentID=abc1234
2026/06/03 13:55:00 INFO Sequin CDC Worker configured sequinURL=http://localhost:7376 consumer=camunda_tasks_stream maxConcurrency=20
2026/06/03 13:55:00 INFO Simulating external loan applications (throttled)...
2026/06/03 13:55:00 INFO Starting Sequin CDC task worker... Press Ctrl+C to stop
2026/06/03 13:55:00 INFO Starting Sequin logical replication worker consumer=camunda_tasks_stream worker_id=loan-worker-cdc max_concurrency=20
2026/06/03 13:55:01 INFO Loan application started businessKey=loan-cdc-... processInstanceID=...
2026/06/03 13:55:01 INFO All loan applications submitted
2026/06/03 13:55:02 INFO Logical CDC lock acquired on task via REST task_id=... topic=creditScoreChecker
2026/06/03 13:55:04 INFO Task processed successfully via Sequin task_id=...
...
```

Press `Ctrl+C` to stop the worker gracefully.

## Code Reuse & Compatibility

This example directly imports and reuses the handlers and in-memory store from the standard `examples/loan-granting` package. This demonstrates that handlers implementing the `camunda.TaskHandler` interface are **100% compatible** and can be run transparently with either the standard `camunda.Worker` or `camunda.SequinWorker`.
