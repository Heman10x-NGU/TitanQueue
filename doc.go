// Copyright 2024 Hemant. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

/*
Package titanqueue provides a distributed task queue backed by Redis.

TitanQueue is a production-ready distributed task queue in Go. It is designed for reliability
with at-least-once delivery semantics, powered by Redis.

# Features

Core Features:
  - At-Least-Once Delivery: Lease-based task ownership with automatic recovery
  - Delayed/Scheduled Tasks: Schedule tasks to run at a specific time
  - Concurrency Control: Configurable worker pool with per-server limits
  - Retry with Exponential Backoff: Customizable retry strategy for failed tasks

Bonus Features:
  - Priority Queues: Weighted queue processing with strict priority option
  - Task Uniqueness: Prevent duplicate task processing with TTL-based locks
  - Task Timeout: Per-task execution limits with context cancellation
  - Graceful Shutdown: Clean termination on OS signals

# Quick Start

Client (Enqueue Tasks):

	client := titanqueue.NewClient(titanqueue.RedisClientOpt{
		Addr: "localhost:6379",
	})
	defer client.Close()

	payload, _ := json.Marshal(map[string]int{"user_id": 42})
	task := titanqueue.NewTask("email:welcome", payload)
	info, err := client.Enqueue(task)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Enqueued: %s", info.ID)

Server (Process Tasks):

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
		return nil
	})

	if err := srv.Run(handler); err != nil {
		log.Fatal(err)
	}

# Task Options

Available options for NewTask and Enqueue:

	MaxRetry(n)      - Maximum retry attempts
	Queue(name)      - Target queue name
	Timeout(d)       - Task execution timeout
	Deadline(t)      - Absolute deadline for task
	ProcessIn(d)     - Delay processing by duration
	ProcessAt(t)     - Schedule at specific time
	Unique(ttl)      - Deduplicate tasks for TTL
	Retention(d)     - Keep completed task for duration
	TaskID(id)       - Custom task ID

# Architecture

TitanQueue uses Redis as the message broker. Tasks are stored in Redis lists (pending, active)
and sorted sets (scheduled, retry, archived, completed). Each task is represented as a hash
containing the task message and metadata.

The Server spawns multiple goroutines:
  - Processor: Worker pool that dequeues and executes tasks
  - Forwarder: Moves scheduled/retry tasks to pending when ready
  - Recoverer: Handles lease-expired tasks from crashed workers
  - Heartbeater: Extends task leases and writes server state
  - Syncer: Retries failed Redis operations
  - Janitor: Cleans up expired completed tasks

# Monitoring

TitanQueue includes a built-in web dashboard. Start it with:

	go run ./ui

Then visit http://localhost:8080 to view queues, tasks, and metrics.
*/
package titanqueue
