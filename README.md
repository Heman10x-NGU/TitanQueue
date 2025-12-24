# TitanQueue

A production-ready distributed task queue built in Go with Redis, designed for reliability through at-least-once delivery semantics.

## Features

### Core Features

- **At-Least-Once Delivery**: Lease-based task ownership with automatic recovery for failed workers
- **Delayed/Scheduled Tasks**: Schedule tasks to run at a specific time or after a delay
- **Concurrency Control**: Configurable worker pool with per-server limits
- **Retry with Exponential Backoff**: Customizable retry strategy for failed tasks

### Bonus Features

- **Priority Queues**: Weighted queue processing with strict priority option
- **Task Uniqueness**: Prevent duplicate task processing with TTL-based locks
- **Task Timeout**: Per-task execution limits with context cancellation
- **Graceful Shutdown**: Clean termination on OS signals
- **Web UI Dashboard**: Built-in TitanQueue Monitor for real-time monitoring

## Quick Start

### 1. Start Redis

```bash
# Using Docker Compose (recommended)
docker-compose up -d

# Or run Redis directly
docker run --name redis -p 6379:6379 -d redis:7-alpine
```

### 2. Create a Client (Enqueue Tasks)

```go
package main

import (
    "encoding/json"
    "log"
    "time"

    "github.com/hemant/titanqueue"
)

func main() {
    client := titanqueue.NewClient(titanqueue.RedisClientOpt{
        Addr: "localhost:6379",
    })
    defer client.Close()

    // Enqueue immediately
    payload, _ := json.Marshal(map[string]int{"user_id": 42})
    task := titanqueue.NewTask("email:welcome", payload)
    info, err := client.Enqueue(task)
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Enqueued: %s", info.ID)

    // Schedule for later
    task = titanqueue.NewTask("report:generate", []byte(`{}`))
    info, err = client.Enqueue(task, titanqueue.ProcessIn(10*time.Minute))
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Scheduled: %s", info.ID)
}
```

### 3. Create a Server (Process Tasks)

```go
package main

import (
    "context"
    "log"

    "github.com/hemant/titanqueue"
)

func main() {
    srv := titanqueue.NewServer(
        titanqueue.RedisClientOpt{Addr: "localhost:6379"},
        titanqueue.Config{
            Concurrency: 10,
            Queues: map[string]int{
                "critical": 6,
                "default":  3,
                "low":      1,
            },
        },
    )

    handler := titanqueue.HandlerFunc(func(ctx context.Context, task *titanqueue.Task) error {
        log.Printf("Processing task: %s", task.Type())
        // Process the task...
        return nil
    })

    if err := srv.Run(handler); err != nil {
        log.Fatal(err)
    }
}
```

## Task Options

| Option         | Description                      |
| -------------- | -------------------------------- |
| `MaxRetry(n)`  | Maximum retry attempts           |
| `Queue(name)`  | Target queue name                |
| `Timeout(d)`   | Task execution timeout           |
| `Deadline(t)`  | Absolute deadline for task       |
| `ProcessIn(d)` | Delay processing by duration     |
| `ProcessAt(t)` | Schedule at specific time        |
| `Unique(ttl)`  | Deduplicate tasks for TTL        |
| `Retention(d)` | Keep completed task for duration |
| `TaskID(id)`   | Custom task ID                   |

## Server Configuration

```go
titanqueue.Config{
    // Worker pool size
    Concurrency: 10,

    // Queue priorities (weighted)
    Queues: map[string]int{
        "critical": 6,
        "default":  3,
        "low":      1,
    },

    // Process highest priority queue first
    StrictPriority: false,

    // Custom retry delay
    RetryDelayFunc: func(n int, e error, t *titanqueue.Task) time.Duration {
        return time.Duration(n*n) * time.Second
    },

    // Error handler
    ErrorHandler: titanqueue.ErrorHandlerFunc(func(ctx context.Context, task *titanqueue.Task, err error) {
        log.Printf("Task %s failed: %v", task.Type(), err)
    }),

    // Graceful shutdown timeout
    ShutdownTimeout: 10 * time.Second,

    // Health check callback
    HealthCheckFunc: func(err error) {
        if err != nil {
            log.Printf("Redis unhealthy: %v", err)
        }
    },
}
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         TitanQueue                              │
├─────────────────────────────────────────────────────────────────┤
│  Client                                                         │
│  ├── Enqueue tasks to pending queue                            │
│  └── Schedule tasks to scheduled ZSET                           │
├─────────────────────────────────────────────────────────────────┤
│  Server                                                         │
│  ├── Processor: Dequeue and execute tasks (worker pool)        │
│  ├── Forwarder: Move scheduled/retry tasks to pending          │
│  ├── Recoverer: Recover orphaned tasks (lease expired)         │
│  ├── Heartbeater: Extend leases, write server state            │
│  ├── Syncer: Retry failed Redis operations                     │
│  ├── Subscriber: Listen for task cancelations (PubSub)         │
│  └── Janitor: Clean up expired completed tasks                 │
├─────────────────────────────────────────────────────────────────┤
│  Redis Data Structures                                          │
│  ├── asynq:{queue}:pending    (List)   - Ready tasks           │
│  ├── asynq:{queue}:active     (List)   - In-progress           │
│  ├── asynq:{queue}:scheduled  (ZSET)   - Scheduled             │
│  ├── asynq:{queue}:retry      (ZSET)   - Retry queue           │
│  ├── asynq:{queue}:archived   (ZSET)   - Failed tasks          │
│  ├── asynq:{queue}:completed  (ZSET)   - Completed             │
│  ├── asynq:{queue}:lease      (ZSET)   - Task leases           │
│  └── asynq:{queue}:t:{id}     (Hash)   - Task data             │
└─────────────────────────────────────────────────────────────────┘
```

## Monitoring

Start the built-in TitanQueue Monitor:

```bash
go run ./ui
# Open http://localhost:8080
```

The dashboard provides:

- Real-time task counts by state (pending, active, scheduled, completed)
- Queue overview with priority status
- Active server and worker monitoring
- Task details with payload inspection

## Development

```bash
# Build
make build

# Run tests
make test

# Start infrastructure
make run-redis

# Run example
make run-client    # Enqueue tasks
make run-server    # Process tasks

# Start Web UI
go run ./ui
```

## License

MIT License - Based on [hibiken/asynq](https://github.com/hibiken/asynq)
