/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reminders

import (
	"context"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(data))
}

type data struct {
	actors *actors.Actors
	got    chan []byte
}

func (d *data) Setup(t *testing.T) []framework.Option {
	d.got = make(chan []byte, 1)
	d.actors = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(_ http.ResponseWriter, req *http.Request) {
			b, err := ioutil.ReadAll(req.Body)
			assert.NoError(t, err)
			d.got <- b
		}),
	)

	return []framework.Option{
		framework.WithProcesses(d.actors),
	}
}

func (d *data) Run(t *testing.T, ctx context.Context) {
	d.actors.WaitUntilRunning(t, ctx)

	_, err := d.actors.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "1234",
		Name:      "helloworld",
		DueTime:   "0s",
		Period:    "1000s",
		Ttl:       "2000s",
		Data:      []byte("mydata"),
	})
	require.NoError(t, err)

	select {
	case b := <-d.got:
		assert.JSONEq(t, `{"data":"bXlkYXRh","dueTime":"0s","period":"1000s"}`, string(b))
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for reminder")
	}
}
