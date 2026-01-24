/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package disseminator

import (
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator/timeout"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/ptr"
)

func (d *disseminator) handleReportedHost(report *loops.ReportedHost) {
	fmt.Printf(">>HANDLING REPORTED HOST: %#+v\n", report)
	op := report.Host.Operation
	if report.Host.Operation == nil {
		fmt.Printf(">>DOING OLD CLIENT HANDLING: %s\n", d.currentOperation)
		// Special case old clients- this always moves the lock forward.
		op = ptr.Of(d.currentOperation)
	}

	switch *op {
	case v1pb.HostOperation_Report:
		d.handleReportedReport(report.StreamIDx, report.Host)

	case v1pb.HostOperation_Lock:
		d.handleReportedLock(report.StreamIDx)

	case v1pb.HostOperation_Update:
		d.handleReportedUpdate(report.StreamIDx)
	}
}

func (d *disseminator) handleReportedReport(streamIDx uint64, host *v1pb.Host) {
	d.currentVersion++
	d.currentOperation = v1pb.HostOperation_Lock
	d.store.Set(streamIDx, host)

	// TODO: @joshvanl: make timeout duration configurable.
	d.timeoutQ.Enqueue(timeout.NewTimeout(d.currentVersion, time.Second*5))

	for _, s := range d.streams {
		s.currentState = ptr.Of(v1pb.HostOperation_Lock)
		s.loop.Enqueue(&loops.DisseminateLock{
			Version: d.currentVersion,
		})
	}
}

// TODO: @joshvanl: add timeout for the 3 stage locks.
func (d *disseminator) handleReportedLock(streamIDx uint64) {
	stream, ok := d.streams[streamIDx]
	if !ok {
		return
	}

	stream.currentState = ptr.Of(v1pb.HostOperation_Lock)

	if d.allStreamsHaveState(v1pb.HostOperation_Lock) {
		// All streams have locked, move to update phase.
		d.currentOperation = v1pb.HostOperation_Update

		for _, s := range d.streams {
			s.currentState = ptr.Of(v1pb.HostOperation_Update)
			s.loop.Enqueue(&loops.DisseminateUpdate{
				Version: d.currentVersion,
				Tables:  d.store.PlacementTables(),
			})
		}
	}
}

func (d *disseminator) handleReportedUpdate(streamIDx uint64) {
	stream, ok := d.streams[streamIDx]
	if !ok {
		return
	}

	stream.currentState = ptr.Of(v1pb.HostOperation_Update)

	if d.allStreamsHaveState(v1pb.HostOperation_Update) {
		// All streams have updated, dissemination is complete, send out unlocks.
		// TODO: @joshvanl: rename "Report" to "Unlock" to be more clear.
		d.currentOperation = v1pb.HostOperation_Report

		d.timeoutQ.Dequeue(d.currentVersion)

		for _, s := range d.streams {
			s.currentState = ptr.Of(v1pb.HostOperation_Report)
			s.currentVersion = ptr.Of(d.currentVersion)
			s.loop.Enqueue(&loops.DisseminateUnlock{
				Version: d.currentVersion,
			})
		}
	}
}

func (d *disseminator) allStreamsHaveState(state v1pb.HostOperation) bool {
	for _, stream := range d.streams {
		if stream.currentState == nil || *stream.currentState != state {
			return false
		}
	}
	return true
}
