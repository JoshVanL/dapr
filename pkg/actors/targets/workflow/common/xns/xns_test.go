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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeterministicKey_StableForSameInputs(t *testing.T) {
	k1 := DeterministicKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, HopDispatch)
	k2 := DeterministicKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, HopDispatch)
	assert.Equal(t, k1, k2, "same inputs must produce the same key")
	assert.NotEmpty(t, k1)
	assert.Len(t, k1, 64, "SHA-256 hex should be 64 chars")
}

func TestDeterministicKey_ChangesWithEveryField(t *testing.T) {
	base := DeterministicKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, HopDispatch)

	cases := []struct {
		name string
		fn   func() string
	}{
		{"sourceNs", func() string {
			return DeterministicKey("ns-b", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, HopDispatch)
		}},
		{"sourceAppID", func() string {
			return DeterministicKey("ns-a", "app-b", "parent-1", "exec-p1", "child-1", "exec-c1", 7, HopDispatch)
		}},
		{"parentOrchID", func() string {
			return DeterministicKey("ns-a", "app-a", "parent-2", "exec-p1", "child-1", "exec-c1", 7, HopDispatch)
		}},
		{"parentExecID", func() string {
			return DeterministicKey("ns-a", "app-a", "parent-1", "exec-p2", "child-1", "exec-c1", 7, HopDispatch)
		}},
		{"childInstanceID", func() string {
			return DeterministicKey("ns-a", "app-a", "parent-1", "exec-p1", "child-2", "exec-c1", 7, HopDispatch)
		}},
		{"childExecID", func() string {
			return DeterministicKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c2", 7, HopDispatch)
		}},
		{"taskID", func() string {
			return DeterministicKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 8, HopDispatch)
		}},
		{"hop", func() string {
			return DeterministicKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, HopResult)
		}},
	}

	seen := map[string]struct{}{base: {}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.fn()
			assert.NotEqual(t, base, got, "changing %s must change the key", c.name)
			// Also assert no collision with any previously-generated variant:
			// separation between fields is what prevents a
			// terminate+purge+rerun from reusing a stale reminder name.
			_, dup := seen[got]
			assert.False(t, dup, "collision on %s", c.name)
			seen[got] = struct{}{}
		})
	}
}

func TestDeterministicKey_FieldSeparation(t *testing.T) {
	// Concatenation without separators would collide when field boundaries
	// shift: "ab"+"c" == "a"+"bc". The implementation uses a NUL separator
	// between fields to eliminate that class of collision.
	a := DeterministicKey("ab", "c", "", "", "", "", 0, HopDispatch)
	b := DeterministicKey("a", "bc", "", "", "", "", 0, HopDispatch)
	assert.NotEqual(t, a, b, "field boundaries must be significant")
}

// TestDeterministicKey_TerminatePurgeRerun is the headline correctness
// case: a parent that's terminated, purged, and recreated under the same
// instance ID gets a fresh executionId. The resulting key must differ so
// stale target-side reminders for the prior run don't collide with the
// fresh dispatch.
func TestDeterministicKey_TerminatePurgeRerun(t *testing.T) {
	prior := DeterministicKey("ns", "app", "parent-1", "exec-old", "child-1", "exec-co", 1, HopDispatch)
	fresh := DeterministicKey("ns", "app", "parent-1", "exec-new", "child-1", "exec-cn", 1, HopDispatch)
	assert.NotEqual(t, prior, fresh, "rerun must produce a distinct key from the prior run")
}

// TestDeterministicKey_DispatchVsResultDistinct guards against the two
// hops sharing keys for otherwise-identical inputs — they live in the
// same reminder namespace and a collision would conflate the directions.
func TestDeterministicKey_DispatchVsResultDistinct(t *testing.T) {
	d := DeterministicKey("ns", "app", "p", "ep", "c", "ec", 9, HopDispatch)
	r := DeterministicKey("ns", "app", "p", "ep", "c", "ec", 9, HopResult)
	assert.NotEqual(t, d, r, "dispatch and result hops must produce distinct keys")
}
