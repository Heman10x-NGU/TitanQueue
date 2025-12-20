// Example client application for TitanQueue
// This demonstrates how to enqueue tasks with various options.
package main

import (
	"encoding/json"
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

func main() {
	// Create a new client connected to Redis.
	client := titanqueue.NewClient(titanqueue.RedisClientOpt{
		Addr: "localhost:6379",
	})
	defer client.Close()

	// ---------------------------------------
	// Example 1: Enqueue a task immediately
	// ---------------------------------------
	emailPayload, _ := json.Marshal(EmailPayload{
		UserID: 42,
		Email:  "user@example.com",
	})
	task := titanqueue.NewTask("email:welcome", emailPayload)
	info, err := client.Enqueue(task)
	if err != nil {
		log.Fatalf("could not enqueue task: %v", err)
	}
	log.Printf("[Immediate] Enqueued task: id=%s queue=%s", info.ID, info.Queue)

	// ---------------------------------------
	// Example 2: Schedule a task to run in 5 minutes
	// ---------------------------------------
	reportPayload, _ := json.Marshal(ReportPayload{
		ReportID:   "report-123",
		ReportType: "monthly",
	})
	task = titanqueue.NewTask("report:generate", reportPayload)
	info, err = client.Enqueue(task, titanqueue.ProcessIn(5*time.Minute))
	if err != nil {
		log.Fatalf("could not schedule task: %v", err)
	}
	log.Printf("[Scheduled] Task will run in 5 minutes: id=%s", info.ID)

	// ---------------------------------------
	// Example 3: Enqueue with options
	// ---------------------------------------
	task = titanqueue.NewTask("email:reminder", emailPayload)
	info, err = client.Enqueue(task,
		titanqueue.MaxRetry(5),             // Retry up to 5 times
		titanqueue.Timeout(30*time.Second), // Timeout after 30 seconds
		titanqueue.Queue("critical"),       // Use the "critical" queue
		titanqueue.Unique(time.Hour),       // Deduplicate for 1 hour
	)
	if err != nil {
		log.Fatalf("could not enqueue task: %v", err)
	}
	log.Printf("[With Options] Enqueued task: id=%s queue=%s", info.ID, info.Queue)

	// ---------------------------------------
	// Example 4: Task with retention (keep result after completion)
	// ---------------------------------------
	task = titanqueue.NewTask("data:process", []byte(`{"data_id": 99}`))
	info, err = client.Enqueue(task, titanqueue.Retention(24*time.Hour))
	if err != nil {
		log.Fatalf("could not enqueue task: %v", err)
	}
	log.Printf("[Retained] Task result will be kept for 24h: id=%s", info.ID)

	log.Println("All tasks enqueued successfully!")
}
