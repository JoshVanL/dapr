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

package reconciler

import (
	"context"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"k8s.io/utils/clock"
)

// PolicyRecompiler is a callback that atomically replaces the compiled policies.
type PolicyRecompiler func(policies *workflowacl.CompiledPolicies)

// WorkflowAccessPolicyOptions holds options for creating a WorkflowAccessPolicy reconciler.
type WorkflowAccessPolicyOptions struct {
	Loader     loader.Interface
	CompStore  *compstore.ComponentStore
	Recompiler PolicyRecompiler
	Healthz    healthz.Healthz
}

type workflowAccessPolicies struct {
	store      *compstore.ComponentStore
	recompiler PolicyRecompiler
	loader.Loader[wfaclapi.WorkflowAccessPolicy]
}

func NewWorkflowAccessPolicies(opts WorkflowAccessPolicyOptions) *Reconciler[wfaclapi.WorkflowAccessPolicy] {
	r := &Reconciler[wfaclapi.WorkflowAccessPolicy]{
		kind:    "WorkflowAccessPolicy",
		htarget: opts.Healthz.AddTarget("workflowaccesspolicy-reconciler"),
		clock:   clock.RealClock{},
		manager: &workflowAccessPolicies{
			Loader:     opts.Loader.WorkflowAccessPolicies(),
			store:      opts.CompStore,
			recompiler: opts.Recompiler,
		},
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

// recompileAll fetches all policies from the compstore, compiles them, and
// atomically swaps them on the gRPC API.
//
//nolint:unused
func (w *workflowAccessPolicies) recompileAll() {
	policies := w.store.ListWorkflowAccessPolicies()
	compiled := workflowacl.Compile(policies)
	w.recompiler(compiled)
	log.Infof("Recompiled %d workflow access policy resource(s)", len(policies))
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (w *workflowAccessPolicies) update(_ context.Context, policy wfaclapi.WorkflowAccessPolicy) {
	w.store.AddWorkflowAccessPolicy(policy)
	w.recompileAll()
}

//nolint:unused
func (w *workflowAccessPolicies) delete(_ context.Context, policy wfaclapi.WorkflowAccessPolicy) {
	w.store.DeleteWorkflowAccessPolicy(policy.Name)
	w.recompileAll()
}
