// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

//go:build windows

package titanqueue

import (
	"os"
	"os/signal"
)

// waitForSignals waits for signals and handles them.
// It handles SIGTERM and SIGINT on Windows.
func (srv *Server) waitForSignals() {
	srv.logger.Info("Listening for signals...")
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	<-sigs
}
