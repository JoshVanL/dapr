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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
)

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true

// WorkflowAccessPolicy controls which app IDs are permitted to schedule
// specific workflows and activities on a target application.
type WorkflowAccessPolicy struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec WorkflowAccessPolicySpec `json:"spec,omitempty"`

	common.Scoped `json:",inline"`
}

// WorkflowAccessPolicySpec defines the desired state of WorkflowAccessPolicy.
type WorkflowAccessPolicySpec struct {
	// DefaultAction is the action when no rule matches. Defaults to "deny" if omitted.
	// +optional
	DefaultAction PolicyAction `json:"defaultAction,omitempty"`

	// Rules defines ingress rules for which callers can perform which operations.
	// +optional
	Rules []WorkflowAccessPolicyRule `json:"rules,omitempty"`
}

// WorkflowAccessPolicyRule defines a set of callers and the operations they
// are allowed or denied.
type WorkflowAccessPolicyRule struct {
	// Callers that this rule applies to.
	Callers []WorkflowCaller `json:"callers"`

	// Operations that the matched callers are allowed/denied to perform.
	Operations []WorkflowOperationRule `json:"operations"`
}

// WorkflowCaller identifies a calling application.
type WorkflowCaller struct {
	// AppID is the Dapr app ID of the caller.
	AppID string `json:"appID"`
}

// PolicyAction is the action to take: "allow" or "deny".
type PolicyAction string

const (
	PolicyActionAllow PolicyAction = "allow"
	PolicyActionDeny  PolicyAction = "deny"
)

// WorkflowOperationType is the type of operation: "workflow" or "activity".
type WorkflowOperationType string

const (
	WorkflowOperationTypeWorkflow WorkflowOperationType = "workflow"
	WorkflowOperationTypeActivity WorkflowOperationType = "activity"
)

// WorkflowOperation is the specific operation being controlled (e.g., "schedule").
type WorkflowOperation string

const (
	WorkflowOperationSchedule WorkflowOperation = "schedule"
)

// WorkflowOperationRule defines access control for a specific workflow or activity operation.
type WorkflowOperationRule struct {
	// Type is "workflow" or "activity".
	Type WorkflowOperationType `json:"type"`

	// Name is the exact name or glob pattern for the workflow/activity.
	Name string `json:"name"`

	// Operation defaults to "schedule" if omitted.
	// +optional
	Operation *WorkflowOperation `json:"operation,omitempty"`

	// Action is "allow" or "deny".
	Action PolicyAction `json:"action"`
}

// +kubebuilder:object:root=true

// WorkflowAccessPolicyList is a list of WorkflowAccessPolicy resources.
type WorkflowAccessPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []WorkflowAccessPolicy `json:"items"`
}
