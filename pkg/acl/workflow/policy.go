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
	"path"
	"strings"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.acl.workflow")

// CompiledPolicies holds pre-processed workflow access policies for a single
// target app. It is built from one or more WorkflowAccessPolicy resources that
// apply to the app (via scopes).
type CompiledPolicies struct {
	rules []compiledRule
}

type compiledRule struct {
	callerAppIDs map[string]struct{}
	operations   []compiledOp
}

type compiledOp struct {
	opType        OperationType
	pattern       string
	action        wfaclapi.PolicyAction
	literalPrefix int // length of literal prefix before first wildcard
	isExact       bool
}

// Compile builds a CompiledPolicies from a set of WorkflowAccessPolicy
// resources that all apply to the same target app. Returns nil if no policies
// are provided (indicating no access control).
func Compile(policies []wfaclapi.WorkflowAccessPolicy) *CompiledPolicies {
	if len(policies) == 0 {
		return nil
	}

	var rules []compiledRule
	for _, policy := range policies {
		for _, rule := range policy.Spec.Rules {
			cr := compiledRule{
				callerAppIDs: make(map[string]struct{}, len(rule.Callers)),
			}
			for _, caller := range rule.Callers {
				cr.callerAppIDs[caller.AppID] = struct{}{}
			}

			for _, op := range rule.Operations {
				// Validate glob pattern
				if _, err := path.Match(op.Name, ""); err != nil {
					log.Warnf("Invalid glob pattern '%s' in WorkflowAccessPolicy '%s', skipping rule", op.Name, policy.Name)
					continue
				}

				co := compiledOp{
					opType:        OperationType(op.Type),
					pattern:       op.Name,
					action:        op.Action,
					literalPrefix: literalPrefixLen(op.Name),
					isExact:       !containsWildcard(op.Name),
				}
				cr.operations = append(cr.operations, co)
			}

			if len(cr.operations) > 0 {
				rules = append(rules, cr)
			}
		}
	}

	return &CompiledPolicies{rules: rules}
}

// Evaluate checks whether the given caller is allowed to perform the specified
// operation. Returns true if allowed, false if denied.
func (cp *CompiledPolicies) Evaluate(callerAppID string, opType OperationType, opName string) bool {
	if cp == nil {
		// No policies means allow all (backward compatible).
		return true
	}

	var bestMatch *compiledOp
	for i := range cp.rules {
		rule := &cp.rules[i]

		// Check if this rule applies to the caller.
		if len(rule.callerAppIDs) > 0 {
			if _, ok := rule.callerAppIDs[callerAppID]; !ok {
				continue
			}
		}

		for j := range rule.operations {
			op := &rule.operations[j]

			// Check operation type matches.
			if op.opType != opType {
				continue
			}

			// Check name matches glob pattern.
			matched, err := path.Match(op.pattern, opName)
			if err != nil || !matched {
				continue
			}

			// Select the most specific match.
			if bestMatch == nil || isMoreSpecific(op, bestMatch) {
				bestMatch = op
			}
		}
	}

	if bestMatch == nil {
		// No rule matched — default deny when policies exist.
		return false
	}

	return bestMatch.action == wfaclapi.PolicyActionAllow
}

// isMoreSpecific returns true if a is more specific than b.
// Specificity rules (from proposal):
// 1. Longest literal prefix wins.
// 2. Exact match beats wildcard match.
// 3. Deny beats allow at same specificity.
func isMoreSpecific(a, b *compiledOp) bool {
	if a.literalPrefix != b.literalPrefix {
		return a.literalPrefix > b.literalPrefix
	}
	if a.isExact != b.isExact {
		return a.isExact
	}
	// Deny takes precedence over allow at same specificity (fail-closed).
	if a.action != b.action {
		return a.action == wfaclapi.PolicyActionDeny
	}
	return false
}

// literalPrefixLen returns the length of the string before the first wildcard
// character (*, ?, or [).
func literalPrefixLen(pattern string) int {
	for i, c := range pattern {
		if c == '*' || c == '?' || c == '[' {
			return i
		}
	}
	return len(pattern)
}

// containsWildcard returns true if the pattern contains any glob wildcard.
func containsWildcard(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}
