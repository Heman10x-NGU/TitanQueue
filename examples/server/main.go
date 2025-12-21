// Example server application for TitanQueue
// This demonstrates how to process tasks with a handler.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/hemant/titanqueue"
)

// EmailPayload defines the payload for email tasks.
type EmailPayload struct {
	UserID int    `json:"user_id"`
	Email  string `json:"email"`
}

// ReportPayload defines the payload for report generation tasks.
type ReportPayload struct {
	ReportID   string `json:"report_id"`
	ReportType string `json:"report_type"`
}

// TaskHandler processes different types of tasks.
type TaskHandler struct{}

// ProcessTask implements the titanqueue.Handler interface.
func (h *TaskHandler) ProcessTask(ctx context.Context, task *titanqueue.Task) error {
	switch task.Type() {
	case "email:welcome":
		return h.handleWelcomeEmail(ctx, task)
	case "email:reminder":
		return h.handleReminderEmail(ctx, task)
	case "report:generate":
		return h.handleReportGeneration(ctx, task)
	case "data:process":
		return h.handleDataProcessing(ctx, task)
	default:
		return fmt.Errorf("unknown task type: %s", task.Type())
	}
}

func (h *TaskHandler) handleWelcomeEmail(ctx context.Context, task *titanqueue.Task) error {
	var payload EmailPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	log.Printf("Sending welcome email to user %d at %s", payload.UserID, payload.Email)
	// Simulate sending email
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (h *TaskHandler) handleReminderEmail(ctx context.Context, task *titanqueue.Task) error {
	var payload EmailPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	log.Printf("Sending reminder email to user %d at %s", payload.UserID, payload.Email)
	// Simulate sending email
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (h *TaskHandler) handleReportGeneration(ctx context.Context, task *titanqueue.Task) error {
	var payload ReportPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	log.Printf("Generating %s report: %s", payload.ReportType, payload.ReportID)
	// Simulate report generation
	time.Sleep(500 * time.Millisecond)

	// Write result to the task
	result := []byte(fmt.Sprintf(`{"status":"completed","report_id":"%s"}`, payload.ReportID))
	if _, err := task.ResultWriter().Write(result); err != nil {
		log.Printf("Warning: could not write result: %v", err)
	}
	return nil
}

func (h *TaskHandler) handleDataProcessing(ctx context.Context, task *titanqueue.Task) error {
	log.Printf("Processing data: %s", string(task.Payload()))
	// Simulate data processing
	time.Sleep(200 * time.Millisecond)
	return nil
}

func main() {
	// Create a new server connected to Redis.
	srv := titanqueue.NewServer(
		titanqueue.RedisClientOpt{Addr: "localhost:6379"},
		titanqueue.Config{
			// Specify the number of concurrent workers (goroutines) to process tasks.
			Concurrency: 10,

			// Specify multiple queues with different priorities.
			Queues: map[string]int{
				"critical": 6, // Process 60% of the time
				"default":  3, // Process 30% of the time
				"low":      1, // Process 10% of the time
			},

			// If set to true, tasks in the queue with the highest priority
			// is processed first.
			StrictPriority: false,

			// Custom retry delay function using exponential backoff.
			RetryDelayFunc: func(n int, e error, t *titanqueue.Task) time.Duration {
				return time.Duration(n*n) * time.Second
			},

			// Log level.
			LogLevel: titanqueue.InfoLevel,

			// Graceful shutdown timeout.
			ShutdownTimeout: 10 * time.Second,

			// Health check function.
			HealthCheckFunc: func(err error) {
				if err != nil {
					log.Printf("Redis health check failed: %v", err)
				}
			},
		},
	)

	// Create and run the handler.
	handler := &TaskHandler{}
	log.Println("Starting TitanQueue server...")
	if err := srv.Run(handler); err != nil {
		log.Fatalf("could not run server: %v", err)
	}
}
