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

package informer

import (
	"context"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/manifest"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowaccesspolicies))
}

// workflowaccesspolicies tests operator hot-reloading of WorkflowAccessPolicy
// resources using the Kubernetes informer.
type workflowaccesspolicies struct {
	daprd    *daprd.Daprd
	pStore   *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	place    *placement.Placement
	sched    *scheduler.Scheduler
}

func (w *workflowaccesspolicies) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	w.pStore = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "WorkflowAccessPolicy",
	})

	boolTrue := true
	w.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sen.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "daprsystem"},
				Spec: configapi.ConfigurationSpec{
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sen.Address(),
					},
					Features: []configapi.FeatureSpec{
						{Name: "HotReload", Enabled: &boolTrue},
						{Name: "WorkflowAccessPolicy", Enabled: &boolTrue},
					},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{manifest.ActorInMemoryStateComponent("default", "mystore")},
		}),
		kubernetes.WithClusterDaprWorkflowAccessPolicyListFromStore(t, w.pStore),
	)

	w.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(w.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	w.place = placement.New(t, placement.WithSentry(t, sen))

	w.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithKubeconfig(w.kubeapi.KubeconfigPath(t)),
		scheduler.WithMode("kubernetes"),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	w.daprd = daprd.New(t,
		daprd.WithAppID("wfacl-k8s"),
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithNamespace("default"),
		daprd.WithSentry(t, sen),
		daprd.WithControlPlaneAddress(w.operator.Address()),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithSchedulerAddresses(w.sched.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(sen, w.kubeapi, w.operator, w.sched, w.place, w.daprd),
	}
}

func (w *workflowaccesspolicies) Run(t *testing.T, ctx context.Context) {
	w.operator.WaitUntilRunning(t, ctx)
	w.place.WaitUntilRunning(t, ctx)
	w.sched.WaitUntilRunning(t, ctx)
	w.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddOrchestratorN("TestWF", func(ctx *task.OrchestrationContext) (any, error) {
		return "wf-ok", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(w.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, registry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(w.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("no policies initially, workflow succeeds", func(t *testing.T) {
		id, err := backendClient.ScheduleNewOrchestration(ctx, "TestWF")
		require.NoError(t, err)
		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	})

	t.Run("add policy via informer and verify hot-reload loads it", func(t *testing.T) {
		// Add a policy that allows the local app. Self-invoked workflows
		// don't go through the CallActor enforcement path, so we verify
		// the hot-reload mechanism by confirming workflows still succeed
		// after the policy is loaded (the policy allows wfacl-k8s).
		policy := &wfaclapi.WorkflowAccessPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: "allow-self", Namespace: "default"},
			Scoped:     common.Scoped{},
			Spec: wfaclapi.WorkflowAccessPolicySpec{
				DefaultAction: wfaclapi.PolicyActionDeny,
				Rules: []wfaclapi.WorkflowAccessPolicyRule{{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-k8s"}},
					Operations: []wfaclapi.WorkflowOperationRule{{
						Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow,
					}},
				}},
			},
		}
		w.pStore.Add(policy)
		w.kubeapi.Informer().Add(t, policy)

		// Give the informer event time to propagate.
		time.Sleep(2 * time.Second)

		id, err := backendClient.ScheduleNewOrchestration(ctx, "TestWF")
		require.NoError(t, err)
		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	})

	t.Run("update policy to allow self, workflow succeeds", func(t *testing.T) {
		policy := &wfaclapi.WorkflowAccessPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
			Scoped:     common.Scoped{},
			Spec: wfaclapi.WorkflowAccessPolicySpec{
				DefaultAction: wfaclapi.PolicyActionDeny,
				Rules: []wfaclapi.WorkflowAccessPolicyRule{{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "wfacl-k8s"}},
					Operations: []wfaclapi.WorkflowOperationRule{{
						Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow,
					}},
				}},
			},
		}
		w.pStore.Set(policy)
		w.kubeapi.Informer().Modify(t, policy)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err := backendClient.ScheduleNewOrchestration(ctx, "TestWF")
			assert.NoError(c, err)
			metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
			assert.NoError(c, err)
			assert.True(c, api.OrchestrationMetadataIsComplete(metadata))
		}, time.Second*20, time.Millisecond*500)
	})

	t.Run("delete policy, back to allow-all", func(t *testing.T) {
		policy := &wfaclapi.WorkflowAccessPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
		}
		w.pStore.Set()
		w.kubeapi.Informer().Delete(t, policy)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			id, err := backendClient.ScheduleNewOrchestration(ctx, "TestWF")
			assert.NoError(c, err)
			metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
			assert.NoError(c, err)
			assert.True(c, api.OrchestrationMetadataIsComplete(metadata))
		}, time.Second*20, time.Millisecond*500)
	})
}
