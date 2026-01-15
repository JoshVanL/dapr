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

package fork

import (
	"fmt"

	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type AfterOptions struct {
	AppID             string
	ActorType         string
	ActivityActorType string
	InstanceID        string
	NewInstanceID     string
	TargetEventID     int32

	OverwriteOutput bool
	Output          *wrapperspb.StringValue

	OldState *state.State
}

type After struct {
	instanceID    string
	newInstanceID string

	oldState      *state.State
	targetEventID int32
	newState      *state.State

	overwriteOutput bool
	output          *wrapperspb.StringValue

	unfinishedActivities     map[int32]*backend.HistoryEvent
	activeTimers             map[int32]*backend.HistoryEvent
	unfinishedChildWorkflows map[int32]*backend.HistoryEvent
}

func NewAfter(opts AfterOptions) *After {
	return &After{
		instanceID:    opts.InstanceID,
		newInstanceID: opts.NewInstanceID,
		oldState:      opts.OldState,
		targetEventID: opts.TargetEventID,
		newState: state.NewState(state.Options{
			AppID:             opts.AppID,
			WorkflowActorType: opts.ActorType,
			ActivityActorType: opts.ActivityActorType,
		}),
		overwriteOutput:          opts.OverwriteOutput,
		output:                   opts.Output,
		unfinishedActivities:     make(map[int32]*backend.HistoryEvent),
		activeTimers:             make(map[int32]*backend.HistoryEvent),
		unfinishedChildWorkflows: make(map[int32]*backend.HistoryEvent),
	}
}

func (f *After) Build() (*state.State, error) {
	var found *protos.HistoryEvent
	var targetHistory *protos.HistoryEvent
	for i, his := range f.oldState.History {
		if his.GetEventId() != f.targetEventID {
			f.handleBefore(his)
			continue
		}

		targetHistory = his
		var err error
		found, err = f.handleFound(i, his)
		if err != nil {
			return nil, err
		}

		break
	}

	if found == nil {
		return nil, status.Errorf(codes.NotFound, "does not have history event with ID '%d'", f.targetEventID)
	}

	// Ensure incomplete activities are also rerun.
	for _, unfin := range f.unfinishedActivities {
		f.newState.AddToInbox(unfin)
	}

	// Ensure incomplete timers are also rerun.
	for _, unfin := range f.activeTimers {
		f.newState.AddToInbox(unfin)
	}

	// Ensure incomplete child workflows are also rerun.
	for _, unfin := range f.unfinishedChildWorkflows {
		sub := unfin.GetSubOrchestrationInstanceCreated()
		sub.InstanceId = fmt.Sprintf("%s:%04x", f.newInstanceID, unfin.EventId)
		f.newState.AddToInbox(unfin)
	}

	f.newState.AddToHistory(targetHistory)
	f.newState.AddToHistory(found)
	for _, h := range f.newState.History {
		fmt.Printf(">>HISTORY: %T\n", h.GetEventType())
	}

	return f.newState, nil
}

func (f *After) handleBefore(his *backend.HistoryEvent) {
	// Track activities which have not been completed yet so they are also
	// rerun.
	switch his.GetEventType().(type) {
	case *protos.HistoryEvent_TaskScheduled:
		f.unfinishedActivities[his.GetEventId()] = his

	case *protos.HistoryEvent_TaskCompleted:
		f.newState.AddToHistory(f.unfinishedActivities[his.GetTaskCompleted().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedActivities, his.GetTaskCompleted().GetTaskScheduledId())

	case *protos.HistoryEvent_TaskFailed:
		f.newState.AddToHistory(f.unfinishedActivities[his.GetTaskFailed().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedActivities, his.GetTaskFailed().GetTaskScheduledId())

	case *protos.HistoryEvent_TimerCreated:
		f.activeTimers[his.GetEventId()] = his

	case *protos.HistoryEvent_SubOrchestrationInstanceCreated:
		f.unfinishedChildWorkflows[his.GetEventId()] = his

	case *protos.HistoryEvent_SubOrchestrationInstanceCompleted:
		f.newState.AddToHistory(f.unfinishedChildWorkflows[his.GetSubOrchestrationInstanceCompleted().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedChildWorkflows, his.GetSubOrchestrationInstanceCompleted().GetTaskScheduledId())

	case *protos.HistoryEvent_SubOrchestrationInstanceFailed:
		f.newState.AddToHistory(f.unfinishedChildWorkflows[his.GetSubOrchestrationInstanceFailed().GetTaskScheduledId()])
		f.newState.AddToHistory(his)
		delete(f.unfinishedChildWorkflows, his.GetSubOrchestrationInstanceFailed().GetTaskScheduledId())

	default:
		f.newState.AddToHistory(his)
	}
}

func (f *After) handleFound(i int, found *backend.HistoryEvent) (*protos.HistoryEvent, error) {
	switch found.GetEventType().(type) {
	case *protos.HistoryEvent_TaskScheduled:
		for _, afterFound := range f.oldState.History[i:] {
			if _, ok := afterFound.GetEventType().(*protos.HistoryEvent_TaskCompleted); ok {
				if afterFound.GetTaskCompleted().TaskScheduledId == found.EventId {
					if f.overwriteOutput {
						afterFound.GetTaskCompleted().Result = f.output
					}

					return afterFound, nil
				}
			}

			f.handleBefore(afterFound)
		}

	case *protos.HistoryEvent_TimerCreated:
		for _, afterFound := range f.oldState.History[i:] {
			if _, ok := afterFound.GetEventType().(*protos.HistoryEvent_TimerFired); ok {
				if afterFound.GetTimerFired().TimerId == found.EventId {
					if f.overwriteOutput {
						return nil, status.Errorf(codes.InvalidArgument, "cannot write output to timer event '%d'", f.targetEventID)
					}

					return afterFound, nil
				}
			}

			f.handleBefore(afterFound)
		}

	case *protos.HistoryEvent_SubOrchestrationInstanceCreated:
		for _, afterFound := range f.oldState.History[i:] {
			if _, ok := afterFound.GetEventType().(*protos.HistoryEvent_SubOrchestrationInstanceCompleted); ok {
				if afterFound.GetSubOrchestrationInstanceCompleted().TaskScheduledId == found.EventId {
					if f.overwriteOutput {
						afterFound.GetSubOrchestrationInstanceCompleted().Result = f.output
					}

					return afterFound, nil
				}
			}

			f.handleBefore(afterFound)
		}

	default:
		return nil, status.Errorf(codes.NotFound, "target event '%T' with ID '%d' is not an event that can be rerun after event", found.GetEventType(), f.targetEventID)
	}

	return nil, status.Errorf(codes.NotFound, "could not find completion event for scheduled event ID '%d'", f.targetEventID)
}
