// Package main provides a web-based monitoring UI for TitanQueue.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// Inspector provides read-only access to TitanQueue data in Redis.
type Inspector struct {
	client redis.UniversalClient
}

// NewInspector creates a new Inspector with the given Redis client.
func NewInspector(client redis.UniversalClient) *Inspector {
	return &Inspector{client: client}
}

// QueueInfo holds information about a queue.
type QueueInfo struct {
	Name      string
	Pending   int64
	Active    int64
	Scheduled int64
	Retry     int64
	Archived  int64
	Completed int64
	Paused    bool
}

// ServerInfo holds information about a running server.
type ServerInfo struct {
	Host              string         `json:"host"`
	PID               int            `json:"pid"`
	ServerID          string         `json:"server_id"`
	Concurrency       int            `json:"concurrency"`
	Queues            map[string]int `json:"queues"`
	StrictPriority    bool           `json:"strict_priority"`
	Status            string         `json:"status"`
	Started           time.Time      `json:"started"`
	ActiveWorkerCount int            `json:"active_worker_count"`
}

// TaskInfo holds information about a task.
type TaskInfo struct {
	ID           string
	Type         string
	Queue        string
	State        string
	Payload      string
	MaxRetry     int
	Retried      int
	LastError    string
	LastFailedAt time.Time
	NextRunAt    time.Time
}

// DashboardStats holds dashboard statistics.
type DashboardStats struct {
	TotalQueues    int
	TotalPending   int64
	TotalActive    int64
	TotalScheduled int64
	TotalRetry     int64
	TotalArchived  int64
	TotalCompleted int64
	ActiveServers  int
	ActiveWorkers  int
}

// GetQueues returns information about all queues.
func (i *Inspector) GetQueues(ctx context.Context) ([]QueueInfo, error) {
	qnames, err := i.client.SMembers(ctx, "asynq:queues").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get queues: %w", err)
	}

	var queues []QueueInfo
	for _, qname := range qnames {
		info, err := i.getQueueInfo(ctx, qname)
		if err != nil {
			continue
		}
		queues = append(queues, info)
	}

	sort.Slice(queues, func(i, j int) bool {
		return queues[i].Name < queues[j].Name
	})

	return queues, nil
}

func (i *Inspector) getQueueInfo(ctx context.Context, qname string) (QueueInfo, error) {
	prefix := fmt.Sprintf("asynq:{%s}:", qname)

	pending, _ := i.client.LLen(ctx, prefix+"pending").Result()
	active, _ := i.client.LLen(ctx, prefix+"active").Result()
	scheduled, _ := i.client.ZCard(ctx, prefix+"scheduled").Result()
	retry, _ := i.client.ZCard(ctx, prefix+"retry").Result()
	archived, _ := i.client.ZCard(ctx, prefix+"archived").Result()
	completed, _ := i.client.ZCard(ctx, prefix+"completed").Result()
	paused, _ := i.client.Exists(ctx, prefix+"paused").Result()

	return QueueInfo{
		Name:      qname,
		Pending:   pending,
		Active:    active,
		Scheduled: scheduled,
		Retry:     retry,
		Archived:  archived,
		Completed: completed,
		Paused:    paused > 0,
	}, nil
}

// GetServers returns information about all active servers.
func (i *Inspector) GetServers(ctx context.Context) ([]ServerInfo, error) {
	keys, err := i.client.Keys(ctx, "asynq:servers:*").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get server keys: %w", err)
	}

	var servers []ServerInfo
	for _, key := range keys {
		data, err := i.client.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var info ServerInfo
		if err := json.Unmarshal([]byte(data), &info); err != nil {
			continue
		}
		servers = append(servers, info)
	}

	return servers, nil
}

// GetDashboardStats returns aggregated statistics for the dashboard.
func (i *Inspector) GetDashboardStats(ctx context.Context) (DashboardStats, error) {
	queues, err := i.GetQueues(ctx)
	if err != nil {
		return DashboardStats{}, err
	}

	var stats DashboardStats
	stats.TotalQueues = len(queues)

	for _, q := range queues {
		stats.TotalPending += q.Pending
		stats.TotalActive += q.Active
		stats.TotalScheduled += q.Scheduled
		stats.TotalRetry += q.Retry
		stats.TotalArchived += q.Archived
		stats.TotalCompleted += q.Completed
	}

	servers, _ := i.GetServers(ctx)
	stats.ActiveServers = len(servers)
	for _, s := range servers {
		stats.ActiveWorkers += s.ActiveWorkerCount
	}

	return stats, nil
}

// GetTasks returns tasks for a specific queue and state.
func (i *Inspector) GetTasks(ctx context.Context, qname, state string, limit int) ([]TaskInfo, error) {
	prefix := fmt.Sprintf("asynq:{%s}:", qname)
	var taskIDs []string
	var scores []float64

	switch state {
	case "pending":
		taskIDs, _ = i.client.LRange(ctx, prefix+"pending", 0, int64(limit-1)).Result()
	case "active":
		taskIDs, _ = i.client.LRange(ctx, prefix+"active", 0, int64(limit-1)).Result()
	case "scheduled":
		results, _ := i.client.ZRangeWithScores(ctx, prefix+"scheduled", 0, int64(limit-1)).Result()
		for _, r := range results {
			taskIDs = append(taskIDs, r.Member.(string))
			scores = append(scores, r.Score)
		}
	case "retry":
		results, _ := i.client.ZRangeWithScores(ctx, prefix+"retry", 0, int64(limit-1)).Result()
		for _, r := range results {
			taskIDs = append(taskIDs, r.Member.(string))
			scores = append(scores, r.Score)
		}
	case "archived":
		results, _ := i.client.ZRangeWithScores(ctx, prefix+"archived", 0, int64(limit-1)).Result()
		for _, r := range results {
			taskIDs = append(taskIDs, r.Member.(string))
			scores = append(scores, r.Score)
		}
	case "completed":
		results, _ := i.client.ZRangeWithScores(ctx, prefix+"completed", 0, int64(limit-1)).Result()
		for _, r := range results {
			taskIDs = append(taskIDs, r.Member.(string))
			scores = append(scores, r.Score)
		}
	}

	var tasks []TaskInfo
	for idx, id := range taskIDs {
		taskKey := prefix + "t:" + id
		data, err := i.client.HGet(ctx, taskKey, "msg").Result()
		if err != nil {
			continue
		}

		var msg struct {
			ID           string `json:"id"`
			Type         string `json:"type"`
			Queue        string `json:"queue"`
			Payload      []byte `json:"payload"`
			Retry        int    `json:"retry"`
			Retried      int    `json:"retried"`
			ErrorMsg     string `json:"error_msg"`
			LastFailedAt int64  `json:"last_failed_at"`
		}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}

		task := TaskInfo{
			ID:        msg.ID,
			Type:      msg.Type,
			Queue:     qname,
			State:     state,
			Payload:   string(msg.Payload),
			MaxRetry:  msg.Retry,
			Retried:   msg.Retried,
			LastError: msg.ErrorMsg,
		}

		if msg.LastFailedAt > 0 {
			task.LastFailedAt = time.Unix(msg.LastFailedAt, 0)
		}

		if idx < len(scores) && scores[idx] > 0 {
			task.NextRunAt = time.Unix(int64(scores[idx]), 0)
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}
