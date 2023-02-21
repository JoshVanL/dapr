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

package security

import (
	"context"
	"crypto/x509"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/assert"
)

func Test_interface(t *testing.T) {
	var _ x509svid.Source = &x509source{}
	var _ x509bundle.Source = &x509source{}
}

func Test_startRotation(t *testing.T) {
	t.Run("if context is cancelled, expect return", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		ch := make(chan struct{})
		go func() {
			defer close(ch)
			startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				t.Error("unexpected call")
				return nil, nil
			}, &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)})
		}()

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("if expiry is in the past, expect renewal to be called", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		ch, renewCalled := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(ch)
			startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				close(renewCalled)
				return &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}, nil
			}, &x509.Certificate{NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: time.Now().Add(-time.Hour)})
		}()

		select {
		case <-time.After(time.Second):
			t.Fatal("expected renewal to be called")
		case <-renewCalled:
			cancel()
		}

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("if expiry is 1 second in the future, expect renew to be called before 3 seconds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		ch, renewCalled := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(ch)
			startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				close(renewCalled)
				return &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}, nil
			}, &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Second)})
		}()

		select {
		case <-time.After(3 * time.Second):
			t.Fatal("expected renewal to be called")
		case <-renewCalled:
			cancel()
		}

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("if expiry is 5 seconds in the future, don't expect renew to be called for first 2 seconds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		ch := make(chan struct{})
		go func() {
			defer close(ch)
			startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				assert.Fail(t, "unexpected renewal to be called")
				return nil, nil
			}, &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Second * 5)})
		}()

		<-time.After(2 * time.Second)
		cancel()

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("renewed certificate should be renewed again when appropriate", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		ch, renewCalled := make(chan struct{}), make(chan struct{})

		go func() {
			defer close(ch)
			var i int
			startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				i++
				if i == 2 {
					close(renewCalled)
				}
				return &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Second / 4)}, nil
			}, &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Second / 4)})
		}()

		select {
		case <-time.After(3 * time.Second):
			t.Fatal("expected renewal to be called multiple times")
		case <-renewCalled:
			cancel()
		}

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})
}

func Test_renewalTime(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		notBefore time.Time
		notAfter  time.Time
		expected  time.Time
	}{
		"not before is in the future": {
			notBefore: now.Add(time.Hour),
			notAfter:  now.Add(2 * time.Hour),
			expected:  now.Add(time.Hour * 17 / 10),
		},
		"not before is in the past": {
			notBefore: now.Add(-time.Hour),
			notAfter:  now.Add(time.Hour),
			expected:  now.Add(time.Hour * 4 / 10),
		},
		"not before is now": {
			notBefore: now,
			notAfter:  now.Add(time.Hour),
			expected:  now.Add(time.Hour * 7 / 10),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actual := renewalTime(test.notBefore, test.notAfter)
			assert.Equal(t, test.expected, actual)
		})
	}
}
