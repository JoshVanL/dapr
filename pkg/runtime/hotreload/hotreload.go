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

package hotreload

import (
	"context"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/healthz"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/disk"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/operator"
	"github.com/dapr/dapr/pkg/runtime/hotreload/reconciler"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload")

type OptionsReloaderDisk struct {
	Config         *config.Configuration
	AppID          string
	Dirs           []string
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Healthz        healthz.Healthz
}

type OptionsReloaderOperator struct {
	PodName        string
	Namespace      string
	Client         operatorv1.OperatorClient
	Config         *config.Configuration
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Healthz        healthz.Healthz
}

type Reloader struct {
	isEnabled               bool
	loader                  loader.Interface
	componentsReconciler    *reconciler.Reconciler[compapi.Component]
	subscriptionsReconciler *reconciler.Reconciler[subapi.Subscription]
	policyReconciler        *reconciler.Reconciler[wfaclapi.WorkflowAccessPolicy]
}

func NewDisk(opts OptionsReloaderDisk) (*Reloader, error) {
	isEnabled := opts.Config.IsFeatureEnabled(config.HotReload)
	if !isEnabled {
		return &Reloader{isEnabled: false}, nil
	}

	loader, err := disk.New(disk.Options{
		AppID:          opts.AppID,
		Dirs:           opts.Dirs,
		ComponentStore: opts.ComponentStore,
	})
	if err != nil {
		return nil, err
	}

	return &Reloader{
		isEnabled: isEnabled,
		loader:    loader,
		componentsReconciler: reconciler.NewComponents(reconciler.Options[compapi.Component]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		subscriptionsReconciler: reconciler.NewSubscriptions(reconciler.Options[subapi.Subscription]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		// policyReconciler is initialized later via SetPolicyRecompiler,
		// since the gRPC API is not available at construction time.
	}, nil
}

func NewOperator(opts OptionsReloaderOperator) *Reloader {
	isEnabled := opts.Config.IsFeatureEnabled(config.HotReload)
	if !isEnabled {
		return &Reloader{isEnabled: false}
	}

	loader := operator.New(operator.Options{
		PodName:        opts.PodName,
		Namespace:      opts.Namespace,
		ComponentStore: opts.ComponentStore,
		OperatorClient: opts.Client,
	})

	return &Reloader{
		isEnabled: isEnabled,
		loader:    loader,
		componentsReconciler: reconciler.NewComponents(reconciler.Options[compapi.Component]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		subscriptionsReconciler: reconciler.NewSubscriptions(reconciler.Options[subapi.Subscription]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
			Healthz:    opts.Healthz,
		}),
		// policyReconciler is initialized later via SetPolicyRecompiler,
		// since the gRPC API is not available at construction time.
	}
}

// Loader returns the underlying loader.Interface used by this reloader.
func (r *Reloader) Loader() loader.Interface {
	return r.loader
}

// SetPolicyRecompiler initializes the WorkflowAccessPolicy reconciler with the
// given recompiler callback. This must be called before Run() when workflow
// access policies are enabled.
func (r *Reloader) SetPolicyRecompiler(opts reconciler.WorkflowAccessPolicyOptions) {
	if !r.isEnabled {
		return
	}

	r.policyReconciler = reconciler.NewWorkflowAccessPolicies(opts)
}

func (r *Reloader) Run(ctx context.Context) error {
	if !r.isEnabled {
		log.Debug("Hot reloading disabled")
		<-ctx.Done()

		return nil
	}

	runners := []func(context.Context) error{
		r.loader.Run,
		r.componentsReconciler.Run,
		r.subscriptionsReconciler.Run,
	}

	if r.policyReconciler != nil {
		runners = append(runners, r.policyReconciler.Run)
		log.Info("Hot reloading enabled. Daprd will reload 'Component', 'Subscription', and 'WorkflowAccessPolicy' resources on change.")
	} else {
		log.Info("Hot reloading enabled. Daprd will reload 'Component' and 'Subscription' resources on change.")
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}
