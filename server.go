// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package titanqueue

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hemant/titanqueue/internal/base"
	"github.com/hemant/titanqueue/internal/log"
	"github.com/hemant/titanqueue/internal/rdb"
	"github.com/redis/go-redis/v9"
)

// Server is responsible for task processing and task lifecycle management.
//
// Server pulls tasks off queues and processes them.
// If the processing of a task is unsuccessful, server will schedule it for a retry.
//
// A task will be retried until either the task gets processed successfully
// or until it reaches its max retry count.
//
// If a task exhausts its retries, it will be moved to the archive and
// will be kept in the archive set.
type Server struct {
	logger *log.Logger

	broker base.Broker
	// When a Server has been created with an existing Redis connection, we do
	// not want to close it.
	sharedConnection bool

	state *serverState

	// wait group to wait for all goroutines to finish.
	wg            sync.WaitGroup
	forwarder     *forwarder
	processor     *processor
	syncer        *syncer
	heartbeater   *heartbeater
	subscriber    *subscriber
	recoverer     *recoverer
	healthchecker *healthchecker
	janitor       *janitor
}

type serverState struct {
	mu    sync.Mutex
	value serverStateValue
}

type serverStateValue int

const (
	// StateNew represents a new server.
	srvStateNew serverStateValue = iota

	// StateActive indicates the server is up and active.
	srvStateActive

	// StateStopped indicates the server is up but no longer processing new tasks.
	srvStateStopped

	// StateClosed indicates the server has been shutdown.
	srvStateClosed
)

var serverStates = []string{
	"new",
	"active",
	"stopped",
	"closed",
}

func (s serverStateValue) String() string {
	if srvStateNew <= s && s <= srvStateClosed {
		return serverStates[s]
	}
	return "unknown status"
}

// Config specifies the server's background-task processing behavior.
type Config struct {
	// Maximum number of concurrent processing of tasks.
	//
	// If set to a zero or negative value, NewServer will overwrite the value
	// to the number of CPUs usable by the current process.
	Concurrency int

	// BaseContext optionally specifies a function that returns the base context for Handler invocations on this server.
	//
	// If BaseContext is nil, the default is context.Background().
	BaseContext func() context.Context

	// TaskCheckInterval specifies the interval between checks for new tasks to process when all queues are empty.
	//
	// If unset, zero or a negative value, the interval is set to 1 second.
	TaskCheckInterval time.Duration

	// Function to calculate retry delay for a failed task.
	//
	// By default, it uses exponential backoff algorithm to calculate the delay.
	RetryDelayFunc RetryDelayFunc

	// Predicate function to determine whether the error returned from Handler is a failure.
	// If the function returns false, Server will not increment the retried counter for the task.
	//
	// By default, if the given error is non-nil the function returns true.
	IsFailure func(error) bool

	// List of queues to process with given priority value. Keys are the names of the
	// queues and values are associated priority value.
	//
	// If set to nil or not specified, the server will process only the "default" queue.
	//
	// Priority is treated as follows to avoid starving low priority queues.
	//
	// Example:
	//
	//     Queues: map[string]int{
	//         "critical": 6,
	//         "default":  3,
	//         "low":      1,
	//     }
	//
	// With the above config and given that all queues are not empty, the tasks
	// in "critical", "default", "low" should be processed 60%, 30%, 10% of
	// the time respectively.
	Queues map[string]int

	// StrictPriority indicates whether the queue priority should be treated strictly.
	//
	// If set to true, tasks in the queue with the highest priority is processed first.
	StrictPriority bool

	// ErrorHandler handles errors returned by the task handler.
	ErrorHandler ErrorHandler

	// Logger specifies the logger used by the server instance.
	//
	// If unset, default logger is used.
	Logger Logger

	// LogLevel specifies the minimum log level to enable.
	//
	// If unset, InfoLevel is used by default.
	LogLevel LogLevel

	// ShutdownTimeout specifies the duration to wait to let workers finish their tasks
	// before forcing them to abort when stopping the server.
	//
	// If unset or zero, default timeout of 8 seconds is used.
	ShutdownTimeout time.Duration

	// HealthCheckFunc is called periodically with any errors encountered during ping to the
	// connected redis server.
	HealthCheckFunc func(error)

	// HealthCheckInterval specifies the interval between healthchecks.
	//
	// If unset or zero, the interval is set to 15 seconds.
	HealthCheckInterval time.Duration

	// DelayedTaskCheckInterval specifies the interval between checks run on 'scheduled' and 'retry'
	// tasks, and forwarding them to 'pending' state if they are ready to be processed.
	//
	// If unset or zero, the interval is set to 5 seconds.
	DelayedTaskCheckInterval time.Duration

	// JanitorInterval specifies the average interval of janitor checks for expired completed tasks.
	//
	// If unset or zero, default interval of 8 seconds is used.
	JanitorInterval time.Duration

	// JanitorBatchSize specifies the number of expired completed tasks to be deleted in one run.
	//
	// If unset or zero, default batch size of 100 is used.
	JanitorBatchSize int
}

// An ErrorHandler handles an error occurred during task processing.
type ErrorHandler interface {
	HandleError(ctx context.Context, task *Task, err error)
}

// The ErrorHandlerFunc type is an adapter to allow the use of ordinary functions as a ErrorHandler.
type ErrorHandlerFunc func(ctx context.Context, task *Task, err error)

// HandleError calls fn(ctx, task, err)
func (fn ErrorHandlerFunc) HandleError(ctx context.Context, task *Task, err error) {
	fn(ctx, task, err)
}

// RetryDelayFunc calculates the retry delay duration for a failed task given
// the retry count, error, and the task.
type RetryDelayFunc func(n int, e error, t *Task) time.Duration

// Logger supports logging at various log levels.
type Logger interface {
	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	Fatal(args ...interface{})
}

// LogLevel represents logging level.
type LogLevel int32

const (
	// Note: reserving value zero to differentiate unspecified case.
	level_unspecified LogLevel = iota
	DebugLevel
	InfoLevel
	WarnLevel
	ErrorLevel
	FatalLevel
)

// String is part of the flag.Value interface.
func (l *LogLevel) String() string {
	switch *l {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	case FatalLevel:
		return "fatal"
	}
	panic(fmt.Sprintf("titanqueue: unexpected log level: %v", *l))
}

// Set is part of the flag.Value interface.
func (l *LogLevel) Set(val string) error {
	switch strings.ToLower(val) {
	case "debug":
		*l = DebugLevel
	case "info":
		*l = InfoLevel
	case "warn", "warning":
		*l = WarnLevel
	case "error":
		*l = ErrorLevel
	case "fatal":
		*l = FatalLevel
	default:
		return fmt.Errorf("titanqueue: unsupported log level %q", val)
	}
	return nil
}

func toInternalLogLevel(l LogLevel) log.Level {
	switch l {
	case DebugLevel:
		return log.DebugLevel
	case InfoLevel:
		return log.InfoLevel
	case WarnLevel:
		return log.WarnLevel
	case ErrorLevel:
		return log.ErrorLevel
	case FatalLevel:
		return log.FatalLevel
	}
	panic(fmt.Sprintf("titanqueue: unexpected log level: %v", l))
}

// DefaultRetryDelayFunc is the default RetryDelayFunc used if one is not specified in Config.
// It uses exponential back-off strategy to calculate the retry delay.
func DefaultRetryDelayFunc(n int, e error, t *Task) time.Duration {
	// Formula taken from https://github.com/mperham/sidekiq.
	s := int(math.Pow(float64(n), 4)) + 15 + (rand.IntN(30) * (n + 1))
	return time.Duration(s) * time.Second
}

func defaultIsFailureFunc(err error) bool { return err != nil }

var defaultQueueConfig = map[string]int{
	base.DefaultQueueName: 1,
}

const (
	defaultTaskCheckInterval        = 1 * time.Second
	defaultShutdownTimeout          = 8 * time.Second
	defaultHealthCheckInterval      = 15 * time.Second
	defaultDelayedTaskCheckInterval = 5 * time.Second
	defaultJanitorInterval          = 8 * time.Second
	defaultJanitorBatchSize         = 100
)

// NewServer returns a new Server given a redis connection option
// and server configuration.
func NewServer(r RedisConnOpt, cfg Config) *Server {
	redisClient, ok := r.MakeRedisClient().(redis.UniversalClient)
	if !ok {
		panic(fmt.Sprintf("titanqueue: unsupported RedisConnOpt type %T", r))
	}
	server := NewServerFromRedisClient(redisClient, cfg)
	server.sharedConnection = false
	return server
}

// NewServerFromRedisClient returns a new instance of Server given a redis.UniversalClient
// and server configuration
func NewServerFromRedisClient(c redis.UniversalClient, cfg Config) *Server {
	baseCtxFn := cfg.BaseContext
	if baseCtxFn == nil {
		baseCtxFn = context.Background
	}
	n := cfg.Concurrency
	if n < 1 {
		n = runtime.NumCPU()
	}

	taskCheckInterval := cfg.TaskCheckInterval
	if taskCheckInterval <= 0 {
		taskCheckInterval = defaultTaskCheckInterval
	}

	delayFunc := cfg.RetryDelayFunc
	if delayFunc == nil {
		delayFunc = DefaultRetryDelayFunc
	}
	isFailureFunc := cfg.IsFailure
	if isFailureFunc == nil {
		isFailureFunc = defaultIsFailureFunc
	}
	queues := make(map[string]int)
	for qname, p := range cfg.Queues {
		if err := base.ValidateQueueName(qname); err != nil {
			continue // ignore invalid queue names
		}
		if p > 0 {
			queues[qname] = p
		}
	}
	if len(queues) == 0 {
		queues = defaultQueueConfig
	}
	var qnames []string
	for q := range queues {
		qnames = append(qnames, q)
	}
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout == 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	healthcheckInterval := cfg.HealthCheckInterval
	if healthcheckInterval == 0 {
		healthcheckInterval = defaultHealthCheckInterval
	}
	logger := log.NewLogger(cfg.Logger)
	loglevel := cfg.LogLevel
	if loglevel == level_unspecified {
		loglevel = InfoLevel
	}
	logger.SetLevel(toInternalLogLevel(loglevel))

	rdb := rdb.NewRDB(c)
	starting := make(chan *workerInfo)
	finished := make(chan *base.TaskMessage)
	syncCh := make(chan *syncRequest)
	srvState := &serverState{value: srvStateNew}
	cancels := base.NewCancelations()

	syncer := newSyncer(syncerParams{
		logger:     logger,
		requestsCh: syncCh,
		interval:   5 * time.Second,
	})
	heartbeater := newHeartbeater(heartbeaterParams{
		logger:         logger,
		broker:         rdb,
		interval:       5 * time.Second,
		concurrency:    n,
		queues:         queues,
		strictPriority: cfg.StrictPriority,
		state:          srvState,
		starting:       starting,
		finished:       finished,
	})
	delayedTaskCheckInterval := cfg.DelayedTaskCheckInterval
	if delayedTaskCheckInterval == 0 {
		delayedTaskCheckInterval = defaultDelayedTaskCheckInterval
	}
	forwarder := newForwarder(forwarderParams{
		logger:   logger,
		broker:   rdb,
		queues:   qnames,
		interval: delayedTaskCheckInterval,
	})
	subscriber := newSubscriber(subscriberParams{
		logger:       logger,
		broker:       rdb,
		cancelations: cancels,
	})
	processor := newProcessor(processorParams{
		logger:            logger,
		broker:            rdb,
		retryDelayFunc:    delayFunc,
		taskCheckInterval: taskCheckInterval,
		baseCtxFn:         baseCtxFn,
		isFailureFunc:     isFailureFunc,
		syncCh:            syncCh,
		cancelations:      cancels,
		concurrency:       n,
		queues:            queues,
		strictPriority:    cfg.StrictPriority,
		errHandler:        cfg.ErrorHandler,
		shutdownTimeout:   shutdownTimeout,
		starting:          starting,
		finished:          finished,
	})
	recoverer := newRecoverer(recovererParams{
		logger:         logger,
		broker:         rdb,
		retryDelayFunc: delayFunc,
		isFailureFunc:  isFailureFunc,
		queues:         qnames,
		interval:       1 * time.Minute,
	})
	healthchecker := newHealthChecker(healthcheckerParams{
		logger:          logger,
		broker:          rdb,
		interval:        healthcheckInterval,
		healthcheckFunc: cfg.HealthCheckFunc,
	})

	janitorInterval := cfg.JanitorInterval
	if janitorInterval == 0 {
		janitorInterval = defaultJanitorInterval
	}

	janitorBatchSize := cfg.JanitorBatchSize
	if janitorBatchSize == 0 {
		janitorBatchSize = defaultJanitorBatchSize
	}
	janitor := newJanitor(janitorParams{
		logger:    logger,
		broker:    rdb,
		queues:    qnames,
		interval:  janitorInterval,
		batchSize: janitorBatchSize,
	})
	return &Server{
		logger:           logger,
		broker:           rdb,
		sharedConnection: true,
		state:            srvState,
		forwarder:        forwarder,
		processor:        processor,
		syncer:           syncer,
		heartbeater:      heartbeater,
		subscriber:       subscriber,
		recoverer:        recoverer,
		healthchecker:    healthchecker,
		janitor:          janitor,
	}
}

// A Handler processes tasks.
//
// ProcessTask should return nil if the processing of a task
// is successful.
//
// If ProcessTask returns a non-nil error or panics, the task
// will be retried after delay if retry-count is remaining,
// otherwise the task will be archived.
type Handler interface {
	ProcessTask(context.Context, *Task) error
}

// The HandlerFunc type is an adapter to allow the use of
// ordinary functions as a Handler.
type HandlerFunc func(context.Context, *Task) error

// ProcessTask calls fn(ctx, task)
func (fn HandlerFunc) ProcessTask(ctx context.Context, task *Task) error {
	return fn(ctx, task)
}

// ErrServerClosed indicates that the operation is now illegal because of the server has been shutdown.
var ErrServerClosed = errors.New("titanqueue: Server closed")

// Run starts the task processing and blocks until
// an os signal to exit the program is received. Once it receives
// a signal, it gracefully shuts down all active workers and other
// goroutines to process the tasks.
func (srv *Server) Run(handler Handler) error {
	if err := srv.Start(handler); err != nil {
		return err
	}
	srv.waitForSignals()
	srv.Shutdown()
	return nil
}

// Start starts the worker server. Once the server has started,
// it pulls tasks off queues and starts a worker goroutine for each task
// and then call Handler to process it.
func (srv *Server) Start(handler Handler) error {
	if handler == nil {
		return fmt.Errorf("titanqueue: server cannot run with nil handler")
	}
	srv.processor.handler = handler

	if err := srv.start(); err != nil {
		return err
	}
	srv.logger.Info("Starting processing")

	srv.heartbeater.start(&srv.wg)
	srv.healthchecker.start(&srv.wg)
	srv.subscriber.start(&srv.wg)
	srv.syncer.start(&srv.wg)
	srv.recoverer.start(&srv.wg)
	srv.forwarder.start(&srv.wg)
	srv.processor.start(&srv.wg)
	srv.janitor.start(&srv.wg)
	return nil
}

// Checks server state and returns an error if pre-condition is not met.
// Otherwise it sets the server state to active.
func (srv *Server) start() error {
	srv.state.mu.Lock()
	defer srv.state.mu.Unlock()
	switch srv.state.value {
	case srvStateActive:
		return fmt.Errorf("titanqueue: the server is already running")
	case srvStateStopped:
		return fmt.Errorf("titanqueue: the server is in the stopped state. Waiting for shutdown.")
	case srvStateClosed:
		return ErrServerClosed
	}
	srv.state.value = srvStateActive
	return nil
}

// Shutdown gracefully shuts down the server.
func (srv *Server) Shutdown() {
	srv.state.mu.Lock()
	if srv.state.value == srvStateNew || srv.state.value == srvStateClosed {
		srv.state.mu.Unlock()
		return
	}
	srv.state.value = srvStateClosed
	srv.state.mu.Unlock()

	srv.logger.Info("Starting graceful shutdown")
	srv.forwarder.shutdown()
	srv.processor.shutdown()
	srv.recoverer.shutdown()
	srv.syncer.shutdown()
	srv.subscriber.shutdown()
	srv.janitor.shutdown()
	srv.healthchecker.shutdown()
	srv.heartbeater.shutdown()
	srv.wg.Wait()

	if !srv.sharedConnection {
		srv.broker.Close()
	}
	srv.logger.Info("Exiting")
}

// Stop signals the server to stop pulling new tasks off queues.
func (srv *Server) Stop() {
	srv.state.mu.Lock()
	if srv.state.value != srvStateActive {
		srv.state.mu.Unlock()
		return
	}
	srv.state.value = srvStateStopped
	srv.state.mu.Unlock()

	srv.logger.Info("Stopping processor")
	srv.processor.stop()
	srv.logger.Info("Processor stopped")
}

// Ping performs a ping against the redis connection.
func (srv *Server) Ping() error {
	srv.state.mu.Lock()
	defer srv.state.mu.Unlock()
	if srv.state.value == srvStateClosed {
		return nil
	}

	return srv.broker.Ping()
}
