/*
Copyright 2025 The Dapr Authors
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

package informer

import (
	"context"
	"fmt"
	"strings"

	"github.com/diagridio/go-etcd-cron/api"
	"google.golang.org/protobuf/types/known/anypb"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
)

type Options struct {
	ConsumerSink <-chan *api.InformerEvent
	JobsLoop     loop.Interface[loops.Event]
}

type Informer struct {
	sink <-chan *api.InformerEvent
	loop loop.Interface[loops.Event]
}

func New(opts Options) *Informer {
	return &Informer{
		sink: opts.ConsumerSink,
		loop: opts.JobsLoop,
	}
}

func (i *Informer) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			go func() {
				for range i.sink {
				}
			}()
			return ctx.Err()

		case event := <-i.sink:
			if err := i.handle(ctx, event); err != nil {
				return err
			}
		}
	}
}

func (i *Informer) handle(ctx context.Context, event *api.InformerEvent) error {
	fmt.Printf("Received informer event: %T\n", event.Event)
	switch ev := event.Event.(type) {
	case *api.InformerEvent_Put:
		return i.handlePut(ev.Put)
	case *api.InformerEvent_Delete:
		return i.handleDelete(ev.Delete)
	case *api.InformerEvent_DropAll:
		i.handleDropAll()
	default:
		return fmt.Errorf("unknown informer event type: %T", event.Event)
	}

	return nil
}

func (i *Informer) handlePut(event *api.InformerEventJob) error {
	job, err := i.handleJobEvent(event)
	if err != nil {
		return err
	}

	i.loop.Enqueue(&loops.JobPut{job})

	return nil
}

func (i *Informer) handleDelete(event *api.InformerEventJob) error {
	job, err := i.handleJobEvent(event)
	if err != nil {
		return err
	}

	i.loop.Enqueue(&loops.JobDelete{job})

	return nil
}

func (i *Informer) handleDropAll() {
	i.loop.Enqueue(new(loops.JobDropAll))
}

func (i *Informer) handleJobEvent(event *api.InformerEventJob) (*internalsv1pb.JobEvent, error) {
	meta, err := i.schedulerMeta(event.GetMetadata())
	if err != nil {
		return nil, err
	}

	return &internalsv1pb.JobEvent{
		Name:     event.GetName()[strings.LastIndex(event.GetName(), "||")+2:],
		Key:      event.GetName(),
		Data:     event.GetPayload(),
		Metadata: meta,
	}, nil
}

func (i *Informer) schedulerMeta(a *anypb.Any) (*schedulerv1pb.JobMetadata, error) {
	var meta schedulerv1pb.JobMetadata
	if err := a.UnmarshalTo(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}
