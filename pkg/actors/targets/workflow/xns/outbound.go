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

package xns

import (
	"context"
	"errors"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// outbound performs the source-side leg of the bridge: it asks the
// configured Forwarder to service-invoke the target namespace's daprd
// and returns the response. The outbound xns actor instance lives on the
// app+namespace that received the original API call.
func (x *xns) outbound(ctx context.Context, op *internalsv1pb.ForwardOpRequest) (*internalsv1pb.ForwardOpResponse, error) {
	if x.forwarder == nil {
		return nil, errors.New("xns: outbound forwarder not configured")
	}
	return x.forwarder.Forward(ctx, op)
}
