// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package titanqueue

import (
	"sync"
	"time"

	"github.com/hemant/titanqueue/internal/base"
	"github.com/hemant/titanqueue/internal/log"
)

// janitor is responsible for periodically deleting expired completed tasks.
type janitor struct {
	logger *log.Logger
	broker base.Broker

	// channel to communicate back to the long running "janitor" goroutine.
	done chan struct{}

	// list of queue names.
	queues []string

	// interval between cleanup runs.
	interval time.Duration

	// number of tasks to delete in a single call.
	batchSize int
}

type janitorParams struct {
	logger    *log.Logger
	broker    base.Broker
	queues    []string
	interval  time.Duration
	batchSize int
}

func newJanitor(params janitorParams) *janitor {
	return &janitor{
		logger:    params.logger,
		broker:    params.broker,
		done:      make(chan struct{}),
		queues:    params.queues,
		interval:  params.interval,
		batchSize: params.batchSize,
	}
}

func (j *janitor) shutdown() {
	j.logger.Debug("Janitor shutting down...")
	// Signal the janitor goroutine to stop.
	j.done <- struct{}{}
}

func (j *janitor) start(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(j.interval)
		for {
			select {
			case <-j.done:
				j.logger.Debug("Janitor done")
				timer.Stop()
				return
			case <-timer.C:
				j.exec()
				timer.Reset(j.interval)
			}
		}
	}()
}

func (j *janitor) exec() {
	for _, qname := range j.queues {
		if err := j.broker.DeleteExpiredCompletedTasks(qname, j.batchSize); err != nil {
			j.logger.Errorf("Failed to delete expired completed tasks from queue %q: %v", qname, err)
		}
	}
}
