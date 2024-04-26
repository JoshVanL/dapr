/*
Copyright 2023 The Dapr Authors
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
package actors

import (
	"bytes"
	"context"
	"encoding/gob"
	"io"

	"github.com/dapr/dapr/pkg/actors/internal"
	"google.golang.org/protobuf/types/known/anypb"
)

const InternalActorTypePrefix = "dapr.internal."

// InternalActorFactory is a function that allocates an internal actor.
type InternalActorFactory = func(actorType string, actorID string, actors Actors) InternalActor

// InternalActor represents the interface for invoking an "internal" actor (one which is built into daprd directly).
type InternalActor interface {
	InvokeMethod(ctx context.Context, methodName string, data []byte, metadata map[string][]string) ([]byte, error)
	DeactivateActor(ctx context.Context) error
	InvokeReminder(ctx context.Context, reminder InternalActorReminder, metadata map[string][]string) error
	InvokeTimer(ctx context.Context, timer InternalActorReminder, metadata map[string][]string) error
}

type InternalActorReminder struct {
	Name    string
	Data    *anypb.Any
	DueTime string
	Period  string
}

func newInternalActorReminder(r *internal.Reminder) InternalActorReminder {
	return InternalActorReminder{
		Name:    r.Name,
		Data:    r.Data,
		DueTime: r.DueTime,
		Period:  r.Period.String(),
	}
}

// EncodeInternalActorData encodes result using the encoding/gob format.
func EncodeInternalActorData(result any) ([]byte, error) {
	var data []byte
	if result != nil {
		var resultBuffer bytes.Buffer
		enc := gob.NewEncoder(&resultBuffer)
		if err := enc.Encode(result); err != nil {
			return nil, err
		}
		data = resultBuffer.Bytes()
	}
	return data, nil
}

// DecodeInternalActorData decodes encoding/gob data and stores the result in e.
func DecodeInternalActorData(data io.Reader, e any) error {
	// Decode the data using encoding/gob (https://go.dev/blog/gob)
	dec := gob.NewDecoder(data)
	if err := dec.Decode(e); err != nil {
		return err
	}
	return nil
}
