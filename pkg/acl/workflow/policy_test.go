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

package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

func makePolicy(name string, rules []wfaclapi.WorkflowAccessPolicyRule) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			Rules: rules,
		},
	}
}

func TestCompile_NilWhenNoPolicies(t *testing.T) {
	cp := Compile(nil)
	assert.Nil(t, cp)

	cp = Compile([]wfaclapi.WorkflowAccessPolicy{})
	assert.Nil(t, cp)
}

func TestEvaluate_NilPoliciesAllowAll(t *testing.T) {
	var cp *CompiledPolicies
	assert.True(t, cp.Evaluate("any-app", OperationTypeWorkflow, "AnyWorkflow"))
}

func TestEvaluate_DefaultDenyWhenPoliciesExist(t *testing.T) {
	// Policy with no rules — defaults to deny all.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("deny-all", nil),
	})

	// Even though the policy has no rules, a non-nil CompiledPolicies
	// means policies exist, so the default is deny.
	assert.False(t, cp.Evaluate("any-app", OperationTypeWorkflow, "AnyWorkflow"))
}

func TestEvaluate_AllowSpecificCaller(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "checkout"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "ProcessOrder",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	})

	assert.True(t, cp.Evaluate("checkout", OperationTypeWorkflow, "ProcessOrder"))
	assert.False(t, cp.Evaluate("other-app", OperationTypeWorkflow, "ProcessOrder"))
	assert.False(t, cp.Evaluate("checkout", OperationTypeWorkflow, "OtherWorkflow"))
	assert.False(t, cp.Evaluate("checkout", OperationTypeActivity, "ProcessOrder"))
}

func TestEvaluate_GlobPatterns(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Process*",
						Action: wfaclapi.PolicyActionAllow,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeActivity,
						Name:   "*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, "ProcessOrder"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, "ProcessRefund"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, "CancelOrder"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeActivity, "AnyActivity"))
}

func TestEvaluate_MostSpecificWins(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "*",
						Action: wfaclapi.PolicyActionDeny,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Process*",
						Action: wfaclapi.PolicyActionAllow,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "ProcessSecret",
						Action: wfaclapi.PolicyActionDeny,
					},
				},
			},
		}),
	})

	// "Process*" (prefix len 7) is more specific than "*" (prefix len 0)
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, "ProcessOrder"))
	// Exact match "ProcessSecret" is more specific than glob "Process*"
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, "ProcessSecret"))
	// Wildcard "*" matches but action is deny
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, "CancelOrder"))
}

func TestEvaluate_DenyWinsTies(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Order*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Order*",
						Action: wfaclapi.PolicyActionDeny,
					},
				},
			},
		}),
	})

	// Same specificity, deny wins.
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, "OrderProcess"))
}

func TestEvaluate_MultipleCallers(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{
					{AppID: "app-a"},
					{AppID: "app-b"},
				},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, "Any"))
	assert.True(t, cp.Evaluate("app-b", OperationTypeWorkflow, "Any"))
	assert.False(t, cp.Evaluate("app-c", OperationTypeWorkflow, "Any"))
}

func TestEvaluate_MultiplePoliciesMerged(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("policy1", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "WorkflowA",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
		makePolicy("policy2", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-b"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "WorkflowB",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, "WorkflowA"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, "WorkflowB"))
	assert.True(t, cp.Evaluate("app-b", OperationTypeWorkflow, "WorkflowB"))
	assert.False(t, cp.Evaluate("app-b", OperationTypeWorkflow, "WorkflowA"))
}

func TestEvaluate_InvalidGlobSkipped(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "[invalid",
						Action: wfaclapi.PolicyActionAllow,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "ValidWorkflow",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	})

	// The invalid glob should be skipped, the valid one should still work.
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, "ValidWorkflow"))
}

func TestEvaluate_SelfInvocation(t *testing.T) {
	// An app calling its own workflows must have itself in the callers list.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "my-app"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	})

	assert.True(t, cp.Evaluate("my-app", OperationTypeWorkflow, "SelfWorkflow"))
	assert.False(t, cp.Evaluate("other-app", OperationTypeWorkflow, "SelfWorkflow"))
}

func TestLiteralPrefixLen(t *testing.T) {
	assert.Equal(t, 0, literalPrefixLen("*"))
	assert.Equal(t, 7, literalPrefixLen("Process*"))
	assert.Equal(t, 12, literalPrefixLen("ProcessOrder"))
	assert.Equal(t, 0, literalPrefixLen("?anything"))
	assert.Equal(t, 3, literalPrefixLen("abc[def]"))
}

func TestContainsWildcard(t *testing.T) {
	assert.True(t, containsWildcard("*"))
	assert.True(t, containsWildcard("Process*"))
	assert.True(t, containsWildcard("test?"))
	assert.True(t, containsWildcard("[abc]"))
	assert.False(t, containsWildcard("ExactMatch"))
	assert.False(t, containsWildcard(""))
}
