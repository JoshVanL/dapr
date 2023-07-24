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

package differ

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload.differ")

type Resource interface {
	compapi.Component | httpendapi.HTTPEndpoint
	meta.Resource
}

type Result[T Resource] struct {
	Deleted []T
	Updated []T
	Created []T
}

type LocalRemoteResources[T Resource] struct {
	Local  []T
	Remote []T
}

// Diff returns the difference between the local and remote resources of the
// given kind.
func Diff[T Resource](resources *LocalRemoteResources[T]) *Result[T] {
	if resources == nil ||
		(len(resources.Local) == 0 && len(resources.Remote) == 0) {
		return nil
	}

	// missing are the resources which exist remotely but which don't exist
	// locally or have changed.
	missing := detectDiff(resources.Local, resources.Remote, nil)

	// deleted are the resources which exist locally but which don't exist
	// remotely or have changed.
	deleted := detectDiff(resources.Remote, resources.Local, func(r T) bool {
		if comp, ok := any(r).(compapi.Component); ok {
			// Ignore the built-in Kubernetes secret store and workflow engine.
			if comp.Name == secretstores.BuiltinKubernetesSecretStore &&
				comp.Spec.Type == "secretstores.kubernetes" {
				return true
			}

			if comp.Name == wfengine.ComponentDefinition.Name &&
				comp.Spec.Type == wfengine.ComponentDefinition.Spec.Type {
				return true
			}
		}

		return false
	})

	var result Result[T]

	for i := range deleted {
		if _, ok := missing[deleted[i].GetName()]; !ok {
			result.Deleted = append(result.Deleted, deleted[i])
			log.Infof("Detected %s has been deleted: %s", deleted[i].Kind(), deleted[i].GetName())
		}
	}

	for i := range missing {
		if _, ok := deleted[missing[i].GetName()]; ok {
			result.Updated = append(result.Updated, missing[i])
			log.Infof("Detected %s has been updated: %s", missing[i].Kind(), missing[i].GetName())
		} else {
			result.Created = append(result.Created, missing[i])
			log.Infof("Detected %s has been created: %s", missing[i].Kind(), missing[i].GetName())
		}
	}

	return &result
}

// detectDiff returns a map for resource names to resources where the base
// resource does not exist in the target.
// The returned map contains on the resources in base which don't exist in the
// target.
// If skipTarget is not nil, if it called on target resources, and if returns
// true, will skip checking whether that base resource exists in the target.
func detectDiff[T Resource](base, target []T, skipTarget func(T) bool) map[string]T {
	notExist := make(map[string]T)
	for i := range target {
		if skipTarget != nil && skipTarget(target[i]) {
			continue
		}

		found := false
		for _, tt := range base {
			if areSame(target[i], tt) {
				found = true
				break
			}
		}
		if !found {
			notExist[target[i].GetName()] = target[i]
		}
	}

	return notExist
}

// areSame returns true if the resources have the same functional spec.
func areSame[T Resource](r1, r2 T) bool {
	return reflect.DeepEqual(toComparableObj(r1), toComparableObj(r2))
}

// toComparableObj returns the object but which strips out values which should
// not be compared as they don't change the spec of the resource.
func toComparableObj[T Resource](r T) metav1.Object {
	obj := r.Object()
	obj.SetNamespace("")
	obj.SetGenerateName("")
	obj.SetUID("")
	obj.SetResourceVersion("")
	obj.SetGeneration(0)
	obj.SetSelfLink("")
	obj.SetCreationTimestamp(metav1.Time{})
	obj.SetDeletionTimestamp(nil)
	obj.SetDeletionGracePeriodSeconds(nil)
	obj.SetLabels(nil)
	obj.SetAnnotations(nil)
	obj.SetFinalizers(nil)
	obj.SetOwnerReferences(nil)
	obj.SetManagedFields(nil)
	return obj
}
