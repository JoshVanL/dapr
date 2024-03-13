/*
Copyright 2021 The Dapr Authors
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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apis/components"
	"github.com/dapr/dapr/utils"
)

const (
	Kind    = "Component"
	Version = "v1alpha1"
)

//+genclient
//+kubebuilder:subresource:status
//+kubebuilder:object:root=true

// Component describes an Dapr component type.
type Component struct {
	metav1.TypeMeta `json:",inline"`
	//+optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	//+optional
	Spec ComponentSpec `json:"spec,omitempty"`
	//+optional
	Auth          `json:"auth,omitempty"`
	common.Scoped `json:",inline"`

	// Status of the Component.
	// This is set and managed automatically.
	// Read-only.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	// +optional
	Status ComponentStatus `json:"status,omitempty"`
}

// Kind returns the component kind.
func (Component) Kind() string {
	return "Component"
}

// GetName returns the component name.
func (c Component) GetName() string {
	return c.Name
}

// GetNamespace returns the component namespace.
func (c Component) GetNamespace() string {
	return c.Namespace
}

// LogName returns the name of the component that can be used in logging.
func (c Component) LogName() string {
	return utils.ComponentLogName(c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version)
}

// GetSecretStore returns the name of the secret store.
func (c Component) GetSecretStore() string {
	return c.Auth.SecretStore
}

// NameValuePairs returns the component's metadata as name/value pairs
func (c Component) NameValuePairs() []common.NameValuePair {
	return c.Spec.Metadata
}

// EmptyMetaDeepCopy returns a new instance of the component type with the
// TypeMeta's Kind and APIVersion fields set.
func (c Component) EmptyMetaDeepCopy() metav1.Object {
	n := c.DeepCopy()
	n.TypeMeta = metav1.TypeMeta{
		Kind:       Kind,
		APIVersion: components.GroupName + "/" + Version,
	}
	n.ObjectMeta = metav1.ObjectMeta{Name: c.Name}
	return n
}

// ComponentSpec is the spec for a component.
type ComponentSpec struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	//+optional
	IgnoreErrors bool                   `json:"ignoreErrors"`
	Metadata     []common.NameValuePair `json:"metadata"`
	//+optional
	InitTimeout string `json:"initTimeout"`
}

// Auth represents authentication details for the component.
type Auth struct {
	SecretStore string `json:"secretStore"`
}

//+kubebuilder:object:root=true

// ComponentList is a list of Dapr components.
type ComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Component `json:"items"`
}

// ComponentStatus is the status for a Component resource.
type ComponentStatus struct {
	// Conditions is a list of conditions indicating the state of the Component.
	// Known condition types are (`Ready`).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []ComponentCondition `json:"conditions,omitempty"`

	// AppIDConditions is a list of conditions indicating the state of each app
	// ID consuming the Component.
	// +listType=map
	// +listMapKey=appID
	// +optional
	AppIDConditions []ComponentAppIDCondition `json:"appIDConditions,omitempty"`

	// InitializedAppIDs is a list of app IDs that have all initialized this
	// Component.
	// +optional
	InitializedAppIDs []string `json:"initializedAppIDs,omitempty"`

	// NotInitializedAppIDs is a list of app IDs that have not initialized this
	// Component due to an error.
	// +optional
	NotInitializedAppIDs []string `json:"notInitializedAppIDs,omitempty"`
}

// ComponentStatus is the status for a Component resource.
type ComponentCondition struct {
	// Type of the condition, known values are (`Ready`).
	Type ComponentConditionType `json:"type"`

	// Status of the condition, one of (`True`, `False`, `Unknown`).
	Status common.ConditionStatus `json:"status"`

	// LastTransitionTime is the timestamp corresponding to the last status
	// change of this condition.
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// Reason is a brief machine readable explanation for the condition's last
	// transition.
	// +optional
	Reason *string `json:"reason,omitempty"`

	// Message is a human readable description of the details of the last
	// transition, complementing reason.
	// +optional
	Message *string `json:"message,omitempty"`

	// If set, this represents the .metadata.generation that the condition was
	// set based upon.
	// For instance, if .metadata.generation is currently 12, but the
	// .status.condition[x].observedGeneration is 9, the condition is out of date
	// with respect to the current state of the Component.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ComponentAppIDCondition describes the state of an app ID consuming
// the Component.
type ComponentAppIDCondition struct {
	//AppID is the ID of the app consuming the Component.
	AppID string `json:"appID"`

	// ReplicaConditions is a list of conditions indicating the state of
	// each replica consuming this Component.
	// +listType=map
	// +listMapKey=podName
	ReplicaConditions []ComponentAppIDReplicaCondition `json:"replicaConditions,omitempty"`
}

// ComponentAppIDReplicaCondition describes the state of an app ID replica
// consuming the Component.
type ComponentAppIDReplicaCondition struct {
	// PodName is the name of the pod consuming the Component.
	PodName string `json:"podName"`

	// Type of the condition, known values are (`Init`).
	Type ComponentAppIDReplicaConditionType `json:"type"`

	// Status of the condition, one of (`True`, `False`, `Unknown`).
	Status common.ConditionStatus `json:"status"`

	// LastTransitionTime is the timestamp corresponding to the last status
	// change of this condition.
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// Reason is a brief machine readable explanation for the condition's last
	// transition.
	// Typically empty if no error occurred.
	// +optional
	Reason *string `json:"reason,omitempty"`

	// Message is a human readable description of the details of the last
	// transition, complementing reason.
	// Typically empty if no error occurred.
	// +optional
	Message *string `json:"message,omitempty"`

	// If set, this represents the .metadata.generation that the condition was
	// set based upon.
	// For instance, if .metadata.generation is currently 12, but the
	// .status.condition[x].observedGeneration is 9, the condition is out of date
	// with respect to the current state of the Component.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ComponentConditionType is a type of condition for a Component.
type ComponentConditionType string

const (
	// ComponentConditionTypeReady indicates a condition describing the
	// readiness of the Component.
	ComponentConditionTypeReady ComponentConditionType = "Ready"
)

// ComponentAppIDReplicaConditionType is a type of condition for an app replica
type ComponentAppIDReplicaConditionType string

const (
	// ComponentConditionAppIDReplicaInit indicates a condition describing the
	// initialization state of the Component.
	ComponentConditionAppIDReplicaInit ComponentAppIDReplicaConditionType = "Init"
)
