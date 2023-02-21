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

package ca

import (
	"context"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/config"
)

func Test_selhosted_store(t *testing.T) {
	t.Run("storing file should write to disk with correct permissions", func(t *testing.T) {
		dir := t.TempDir()

		rootFile := filepath.Join(dir + "root.pem")
		issuerFile := filepath.Join(dir + "issuer.pem")
		keyFile := filepath.Join(dir + "key.pem")

		s := &selfhosted{
			config: config.Config{
				RootCertPath:   rootFile,
				IssuerCertPath: issuerFile,
				IssuerKeyPath:  keyFile,
			},
		}

		assert.NoError(t, s.store(nil, caBundle{
			trustAnchors: []byte("root"),
			issChainPEM:  []byte("issuer"),
			issKeyPEM:    []byte("key"),
		}))

		require.FileExists(t, rootFile)
		require.FileExists(t, issuerFile)
		require.FileExists(t, keyFile)

		info, err := os.Stat(rootFile)
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode())

		info, err = os.Stat(issuerFile)
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode())

		info, err = os.Stat(keyFile)
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode())

		b, err := os.ReadFile(rootFile)
		assert.NoError(t, err)
		assert.Equal(t, "root", string(b))

		b, err = os.ReadFile(issuerFile)
		assert.NoError(t, err)
		assert.Equal(t, "issuer", string(b))

		b, err = os.ReadFile(keyFile)
		assert.NoError(t, err)
		assert.Equal(t, "key", string(b))
	})
}

func Test_selfhosted_get(t *testing.T) {
	rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
	intPEM, intCrt, intPKPEM, intPK := genCrt(t, "int", rootCrt, rootPK)

	tests := map[string]struct {
		rootFile  *[]byte
		issuer    *[]byte
		key       *[]byte
		expBundle caBundle
		expOk     bool
		expErr    bool
	}{
		"if no files exist, return not ok": {
			rootFile:  nil,
			issuer:    nil,
			key:       nil,
			expBundle: caBundle{},
			expOk:     false,
			expErr:    false,
		},
		"if root file doesn't exist, return not ok": {
			rootFile:  nil,
			issuer:    &intPEM,
			key:       &intPKPEM,
			expBundle: caBundle{},
			expOk:     false,
			expErr:    false,
		},
		"if issuer file doesn't exist, return not ok": {
			rootFile:  &rootPEM,
			issuer:    nil,
			key:       &intPKPEM,
			expBundle: caBundle{},
			expOk:     false,
			expErr:    false,
		},
		"if issuer key file doesn't exist, return not ok": {
			rootFile:  &rootPEM,
			issuer:    &intPEM,
			key:       nil,
			expBundle: caBundle{},
			expOk:     false,
			expErr:    false,
		},
		"if failed to verify CA bundle, return error": {
			rootFile:  &intPEM,
			issuer:    &intPEM,
			key:       &intPKPEM,
			expBundle: caBundle{},
			expOk:     false,
			expErr:    true,
		},
		"if all files exist, return certs": {
			rootFile: &rootPEM,
			issuer:   &intPEM,
			key:      &intPKPEM,
			expBundle: caBundle{
				trustAnchors: rootPEM,
				issChainPEM:  intPEM,
				issKeyPEM:    intPKPEM,
				issChain:     []*x509.Certificate{intCrt},
				issKey:       intPK,
			},
			expOk:  true,
			expErr: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			rootFile := filepath.Join(dir + "root.pem")
			issuerFile := filepath.Join(dir + "issuer.pem")
			keyFile := filepath.Join(dir + "key.pem")
			s := &selfhosted{
				config: config.Config{
					RootCertPath:   rootFile,
					IssuerCertPath: issuerFile,
					IssuerKeyPath:  keyFile,
				},
			}

			if test.rootFile != nil {
				require.NoError(t, os.WriteFile(rootFile, *test.rootFile, 0600))
			}
			if test.issuer != nil {
				require.NoError(t, os.WriteFile(issuerFile, *test.issuer, 0600))
			}
			if test.key != nil {
				require.NoError(t, os.WriteFile(keyFile, *test.key, 0600))
			}

			caBundle, ok, err := s.get(context.Background())
			assert.Equal(t, test.expBundle, caBundle)
			assert.Equal(t, test.expOk, ok)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
		})
	}
}
