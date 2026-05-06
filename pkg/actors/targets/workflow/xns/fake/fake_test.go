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

package fake_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/xns/fake"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// stubReceiver records the request it received and returns a canned
// response. Used to assert the fake forwarder routes by (ns, appID).
type stubReceiver struct {
	gotReq *internalsv1pb.ForwardOpRequest
	resp   *internalsv1pb.ForwardOpResponse
	err    error
}

func (s *stubReceiver) Execute(ctx context.Context, req *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error) {
	s.gotReq = req
	return s.resp, s.err
}

func TestForwarder_RoutesByTargetNamespaceAndAppID(t *testing.T) {
	hub := fake.NewHub()

	rxA := &stubReceiver{resp: &internalsv1pb.ForwardOpResponse{Payload: []byte("from-A")}}
	rxB := &stubReceiver{resp: &internalsv1pb.ForwardOpResponse{Payload: []byte("from-B")}}
	hub.Register("nsA", "app1", rxA)
	hub.Register("nsB", "app1", rxB)

	f := hub.Forwarder()

	resp, err := f.Forward(context.Background(), &internalsv1pb.ForwardOpRequest{
		TargetAppNamespace: "nsB",
		TargetAppId:        "app1",
		Operation:          internalsv1pb.WorkflowOpKind_WORKFLOW_OP_TERMINATE,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("from-B"), resp.GetPayload())
	assert.NotNil(t, rxB.gotReq)
	assert.Nil(t, rxA.gotReq)
}

func TestForwarder_PropagatesReceiverError(t *testing.T) {
	hub := fake.NewHub()
	rx := &stubReceiver{err: errors.New("boom")}
	hub.Register("nsA", "app1", rx)

	_, err := hub.Forwarder().Forward(context.Background(), &internalsv1pb.ForwardOpRequest{
		TargetAppNamespace: "nsA",
		TargetAppId:        "app1",
	})
	assert.ErrorContains(t, err, "boom")
}

func TestForwarder_UnknownTargetReturnsError(t *testing.T) {
	hub := fake.NewHub()
	_, err := hub.Forwarder().Forward(context.Background(), &internalsv1pb.ForwardOpRequest{
		TargetAppNamespace: "nsX",
		TargetAppId:        "appX",
	})
	assert.ErrorContains(t, err, "no receiver registered")
}

func TestForwarder_RejectsMissingTargetNamespace(t *testing.T) {
	_, err := fake.NewHub().Forwarder().Forward(context.Background(), &internalsv1pb.ForwardOpRequest{
		TargetAppId: "app1",
	})
	assert.ErrorContains(t, err, "target_app_namespace")
}
