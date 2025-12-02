// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

// Package base defines foundational types and constants used in titanqueue package.
package base

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hemant/titanqueue/internal/errors"
	"github.com/hemant/titanqueue/internal/timeutil"
	"github.com/redis/go-redis/v9"
)

// Version of titanqueue library.
const Version = "1.0.0"

// DefaultQueueName is the queue name used if none are specified by user.
const DefaultQueueName = "default"

// DefaultQueue is the redis key for the default queue.
var DefaultQueue = PendingKey(DefaultQueueName)

// Global Redis keys.
// Note: Uses 'asynq:' prefix for compatibility with asynqmon monitoring UI.
const (
	AllServers    = "asynq:servers" // ZSET
	AllWorkers    = "asynq:workers" // ZSET
	AllQueues     = "asynq:queues"  // SET
	CancelChannel = "asynq:cancel"  // PubSub channel
)

// TaskState denotes the state of a task.
type TaskState int

const (
	TaskStateActive TaskState = iota + 1
	TaskStatePending
	TaskStateScheduled
	TaskStateRetry
	TaskStateArchived
	TaskStateCompleted
)

func (s TaskState) String() string {
	switch s {
	case TaskStateActive:
		return "active"
	case TaskStatePending:
		return "pending"
	case TaskStateScheduled:
		return "scheduled"
	case TaskStateRetry:
		return "retry"
	case TaskStateArchived:
		return "archived"
	case TaskStateCompleted:
		return "completed"
	}
	panic(fmt.Sprintf("internal error: unknown task state %d", s))
}

func TaskStateFromString(s string) (TaskState, error) {
	switch s {
	case "active":
		return TaskStateActive, nil
	case "pending":
		return TaskStatePending, nil
	case "scheduled":
		return TaskStateScheduled, nil
	case "retry":
		return TaskStateRetry, nil
	case "archived":
		return TaskStateArchived, nil
	case "completed":
		return TaskStateCompleted, nil
	}
	return 0, errors.E(errors.FailedPrecondition, fmt.Sprintf("%q is not supported task state", s))
}

// ValidateQueueName validates a given qname to be used as a queue name.
// Returns nil if valid, otherwise returns non-nil error.
func ValidateQueueName(qname string) error {
	if len(strings.TrimSpace(qname)) == 0 {
		return fmt.Errorf("queue name must contain one or more characters")
	}
	return nil
}

// QueueKeyPrefix returns a prefix for all keys in the given queue.
// Note: Uses 'asynq:' prefix for compatibility with asynqmon monitoring UI.
func QueueKeyPrefix(qname string) string {
	return "asynq:{" + qname + "}:"
}

// TaskKeyPrefix returns a prefix for task key.
func TaskKeyPrefix(qname string) string {
	return QueueKeyPrefix(qname) + "t:"
}

// TaskKey returns a redis key for the given task message.
func TaskKey(qname, id string) string {
	return TaskKeyPrefix(qname) + id
}

// PendingKey returns a redis key for the given queue name.
func PendingKey(qname string) string {
	return QueueKeyPrefix(qname) + "pending"
}

// ActiveKey returns a redis key for the active tasks.
func ActiveKey(qname string) string {
	return QueueKeyPrefix(qname) + "active"
}

// ScheduledKey returns a redis key for the scheduled tasks.
func ScheduledKey(qname string) string {
	return QueueKeyPrefix(qname) + "scheduled"
}

// RetryKey returns a redis key for the retry tasks.
func RetryKey(qname string) string {
	return QueueKeyPrefix(qname) + "retry"
}

// ArchivedKey returns a redis key for the archived tasks.
func ArchivedKey(qname string) string {
	return QueueKeyPrefix(qname) + "archived"
}

// LeaseKey returns a redis key for the lease.
func LeaseKey(qname string) string {
	return QueueKeyPrefix(qname) + "lease"
}

func CompletedKey(qname string) string {
	return QueueKeyPrefix(qname) + "completed"
}

// PausedKey returns a redis key to indicate that the given queue is paused.
func PausedKey(qname string) string {
	return QueueKeyPrefix(qname) + "paused"
}

// ProcessedTotalKey returns a redis key for total processed count for the given queue.
func ProcessedTotalKey(qname string) string {
	return QueueKeyPrefix(qname) + "processed"
}

// FailedTotalKey returns a redis key for total failure count for the given queue.
func FailedTotalKey(qname string) string {
	return QueueKeyPrefix(qname) + "failed"
}

// ProcessedKey returns a redis key for processed count for the given day for the queue.
func ProcessedKey(qname string, t time.Time) string {
	return QueueKeyPrefix(qname) + "processed:" + t.UTC().Format("2006-01-02")
}

// FailedKey returns a redis key for failure count for the given day for the queue.
func FailedKey(qname string, t time.Time) string {
	return QueueKeyPrefix(qname) + "failed:" + t.UTC().Format("2006-01-02")
}

// ServerInfoKey returns a redis key for process info.
func ServerInfoKey(hostname string, pid int, serverID string) string {
	return fmt.Sprintf("asynq:servers:{%s:%d:%s}", hostname, pid, serverID)
}

// WorkersKey returns a redis key for the workers given hostname, pid, and server ID.
func WorkersKey(hostname string, pid int, serverID string) string {
	return fmt.Sprintf("asynq:workers:{%s:%d:%s}", hostname, pid, serverID)
}

// UniqueKey returns a redis key with the given type, payload, and queue name.
func UniqueKey(qname, tasktype string, payload []byte) string {
	if payload == nil {
		return QueueKeyPrefix(qname) + "unique:" + tasktype + ":"
	}
	checksum := md5.Sum(payload)
	return QueueKeyPrefix(qname) + "unique:" + tasktype + ":" + hex.EncodeToString(checksum[:])
}

// TaskMessage is the internal representation of a task with additional metadata fields.
// Serialized data of this type gets written to redis.
type TaskMessage struct {
	// Type indicates the kind of the task to be performed.
	Type string `json:"type"`

	// Payload holds data needed to process the task.
	Payload []byte `json:"payload"`

	// ID is a unique identifier for each task.
	ID string `json:"id"`

	// Queue is a name this message should be enqueued to.
	Queue string `json:"queue"`

	// Retry is the max number of retry for this task.
	Retry int `json:"retry"`

	// Retried is the number of times we've retried this task so far.
	Retried int `json:"retried"`

	// ErrorMsg holds the error message from the last failure.
	ErrorMsg string `json:"error_msg,omitempty"`

	// Time of last failure in Unix time,
	// the number of seconds elapsed since January 1, 1970 UTC.
	//
	// Use zero to indicate no last failure
	LastFailedAt int64 `json:"last_failed_at,omitempty"`

	// Timeout specifies timeout in seconds.
	// If task processing doesn't complete within the timeout, the task will be retried
	// if retry count is remaining. Otherwise it will be moved to the archive.
	//
	// Use zero to indicate no timeout.
	Timeout int64 `json:"timeout,omitempty"`

	// Deadline specifies the deadline for the task in Unix time,
	// the number of seconds elapsed since January 1, 1970 UTC.
	// If task processing doesn't complete before the deadline, the task will be retried
	// if retry count is remaining. Otherwise it will be moved to the archive.
	//
	// Use zero to indicate no deadline.
	Deadline int64 `json:"deadline,omitempty"`

	// UniqueKey holds the redis key used for uniqueness lock for this task.
	//
	// Empty string indicates that no uniqueness lock was used.
	UniqueKey string `json:"unique_key,omitempty"`

	// Retention specifies the number of seconds the task should be retained after completion.
	Retention int64 `json:"retention,omitempty"`

	// CompletedAt is the time the task was processed successfully in Unix time,
	// the number of seconds elapsed since January 1, 1970 UTC.
	//
	// Use zero to indicate no value.
	CompletedAt int64 `json:"completed_at,omitempty"`
}

// EncodeMessage marshals the given task message and returns an encoded bytes.
func EncodeMessage(msg *TaskMessage) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("cannot encode nil message")
	}
	return json.Marshal(msg)
}

// DecodeMessage unmarshals the given bytes and returns a decoded task message.
func DecodeMessage(data []byte) (*TaskMessage, error) {
	var msg TaskMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// TaskInfo describes a task message and its metadata.
type TaskInfo struct {
	Message       *TaskMessage
	State         TaskState
	NextProcessAt time.Time
	Result        []byte
}

// Z represents sorted set member.
type Z struct {
	Message *TaskMessage
	Score   int64
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

// EncodeServerInfo marshals the given ServerInfo and returns the encoded bytes.
func EncodeServerInfo(info *ServerInfo) ([]byte, error) {
	if info == nil {
		return nil, fmt.Errorf("cannot encode nil server info")
	}
	return json.Marshal(info)
}

// DecodeServerInfo decodes the given bytes into ServerInfo.
func DecodeServerInfo(b []byte) (*ServerInfo, error) {
	var info ServerInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// WorkerInfo holds information about a running worker.
type WorkerInfo struct {
	Host     string    `json:"host"`
	PID      int       `json:"pid"`
	ServerID string    `json:"server_id"`
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Payload  []byte    `json:"payload"`
	Queue    string    `json:"queue"`
	Started  time.Time `json:"started"`
	Deadline time.Time `json:"deadline"`
}

// EncodeWorkerInfo marshals the given WorkerInfo and returns the encoded bytes.
func EncodeWorkerInfo(info *WorkerInfo) ([]byte, error) {
	if info == nil {
		return nil, fmt.Errorf("cannot encode nil worker info")
	}
	return json.Marshal(info)
}

// DecodeWorkerInfo decodes the given bytes into WorkerInfo.
func DecodeWorkerInfo(b []byte) (*WorkerInfo, error) {
	var info WorkerInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Cancelations is a collection that holds cancel functions for all active tasks.
//
// Cancelations are safe for concurrent use by multiple goroutines.
type Cancelations struct {
	mu          sync.Mutex
	cancelFuncs map[string]context.CancelFunc
}

// NewCancelations returns a Cancelations instance.
func NewCancelations() *Cancelations {
	return &Cancelations{
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

// Add adds a new cancel func to the collection.
func (c *Cancelations) Add(id string, fn context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelFuncs[id] = fn
}

// Delete deletes a cancel func from the collection given an id.
func (c *Cancelations) Delete(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cancelFuncs, id)
}

// Get returns a cancel func given an id.
func (c *Cancelations) Get(id string) (fn context.CancelFunc, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn, ok = c.cancelFuncs[id]
	return fn, ok
}

// Lease is a time bound lease for worker to process task.
// It provides a communication channel between lessor and lessee about lease expiration.
type Lease struct {
	once sync.Once
	ch   chan struct{}

	Clock timeutil.Clock

	mu       sync.Mutex
	expireAt time.Time // guarded by mu
}

func NewLease(expirationTime time.Time) *Lease {
	return &Lease{
		ch:       make(chan struct{}),
		expireAt: expirationTime,
		Clock:    timeutil.NewRealClock(),
	}
}

// Reset changes the lease to expire at the given time.
// It returns true if the lease is still valid and reset operation was successful, false if the lease had been expired.
func (l *Lease) Reset(expirationTime time.Time) bool {
	if !l.IsValid() {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expireAt = expirationTime
	return true
}

// NotifyExpiration sends a notification to lessee about expired lease
// Returns true if notification was sent, returns false if the lease is still valid and notification was not sent.
func (l *Lease) NotifyExpiration() bool {
	if l.IsValid() {
		return false
	}
	l.once.Do(l.closeCh)
	return true
}

func (l *Lease) closeCh() {
	close(l.ch)
}

// Done returns a communication channel from which the lessee can read to get notified when lessor notifies about lease expiration.
func (l *Lease) Done() <-chan struct{} {
	return l.ch
}

// Deadline returns the expiration time of the lease.
func (l *Lease) Deadline() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expireAt
}

// IsValid returns true if the lease's expiration time is in the future or equals to the current time,
// returns false otherwise.
func (l *Lease) IsValid() bool {
	now := l.Clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expireAt.After(now) || l.expireAt.Equal(now)
}

// Broker is a message broker that supports operations to manage task queues.
//
// See rdb.RDB as a reference implementation.
type Broker interface {
	Ping() error
	Close() error
	Enqueue(ctx context.Context, msg *TaskMessage) error
	EnqueueUnique(ctx context.Context, msg *TaskMessage, ttl time.Duration) error
	Dequeue(qnames ...string) (*TaskMessage, time.Time, error)
	Done(ctx context.Context, msg *TaskMessage) error
	MarkAsComplete(ctx context.Context, msg *TaskMessage) error
	Requeue(ctx context.Context, msg *TaskMessage) error
	Schedule(ctx context.Context, msg *TaskMessage, processAt time.Time) error
	ScheduleUnique(ctx context.Context, msg *TaskMessage, processAt time.Time, ttl time.Duration) error
	Retry(ctx context.Context, msg *TaskMessage, processAt time.Time, errMsg string, isFailure bool) error
	Archive(ctx context.Context, msg *TaskMessage, errMsg string) error
	ForwardIfReady(qnames ...string) error

	// Task retention related method
	DeleteExpiredCompletedTasks(qname string, batchSize int) error

	// Lease related methods
	ListLeaseExpired(cutoff time.Time, qnames ...string) ([]*TaskMessage, error)
	ExtendLease(qname string, ids ...string) (time.Time, error)

	// State snapshot related methods
	WriteServerState(info *ServerInfo, workers []*WorkerInfo, ttl time.Duration) error
	ClearServerState(host string, pid int, serverID string) error

	// Cancelation related methods
	CancelationPubSub() (*redis.PubSub, error)
	PublishCancelation(id string) error

	WriteResult(qname, id string, data []byte) (n int, err error)
}
