// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package titanqueue

import (
	"sync"
	"time"

	"github.com/hemant/titanqueue/internal/base"
	"github.com/hemant/titanqueue/internal/log"
)

// healthchecker is responsible for periodically checking the health of the
// redis server and invoking a user provided callback if the server is down.
type healthchecker struct {
	logger *log.Logger
	broker base.Broker

	// channel to communicate back to the long running "healthchecker" goroutine.
	done chan struct{}

	// interval between healthchecks.
	interval time.Duration

	// user provided callback to invoke if the server is down.
	healthcheckFunc func(error)
}

type healthcheckerParams struct {
	logger          *log.Logger
	broker          base.Broker
	interval        time.Duration
	healthcheckFunc func(error)
}

func newHealthChecker(params healthcheckerParams) *healthchecker {
	return &healthchecker{
		logger:          params.logger,
		broker:          params.broker,
		done:            make(chan struct{}),
		interval:        params.interval,
		healthcheckFunc: params.healthcheckFunc,
	}
}

func (hc *healthchecker) shutdown() {
	hc.logger.Debug("Healthchecker shutting down...")
	// Signal the healthchecker goroutine to stop.
	hc.done <- struct{}{}
}

func (hc *healthchecker) start(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(hc.interval)
		for {
			select {
			case <-hc.done:
				hc.logger.Debug("Healthchecker done")
				timer.Stop()
				return
			case <-timer.C:
				hc.exec()
				timer.Reset(hc.interval)
			}
		}
	}()
}

func (hc *healthchecker) exec() {
	err := hc.broker.Ping()
	if hc.healthcheckFunc != nil {
		hc.healthcheckFunc(err)
	}
}
