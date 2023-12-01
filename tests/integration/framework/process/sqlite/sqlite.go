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

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	// Blank import for the sqlite driver
	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsv1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

// Option is a function that configures the process.
type Option func(*options)

// SQLite database that can be used in integration tests.
type SQLite struct {
	dbPath            string
	componentJSON     []byte
	createStateTables bool
	execs             []string
}

func New(t *testing.T, fopts ...Option) *SQLite {
	t.Helper()

	opts := options{
		name:     "mystore",
		metadata: make(map[string]string),
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	dbPath := filepath.Join(t.TempDir(), "test-data.db")

	c := componentsv1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Component",
			APIVersion: "dapr.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: opts.name,
		},
		Spec: componentsv1alpha1.ComponentSpec{
			Type:    "state.sqlite",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{Name: "connectionString", Value: toDynamicValue(t, "file:"+dbPath)},
				{Name: "actorStateStore", Value: toDynamicValue(t, strconv.FormatBool(opts.actorStateStore))},
			},
		},
	}

	for k, v := range opts.metadata {
		c.Spec.Metadata = append(c.Spec.Metadata, commonapi.NameValuePair{
			Name:  k,
			Value: toDynamicValue(t, v),
		})
	}

	compJSON, err := json.Marshal(c)
	require.NoError(t, err)

	return &SQLite{
		dbPath:            dbPath,
		createStateTables: opts.createStateTables,
		componentJSON:     compJSON,
		execs:             opts.execs,
	}
}

func (s *SQLite) Run(t *testing.T, ctx context.Context) {
	_, err := s.GetConnection(t).Exec(`
CREATE TABLE metadata (
  key text NOT NULL PRIMARY KEY,
  value text NOT NULL
);
INSERT INTO metadata VALUES('migrations','1');
CREATE TABLE state (
  key TEXT NOT NULL PRIMARY KEY,
  value TEXT NOT NULL,
  is_binary BOOLEAN NOT NULL,
  etag TEXT NOT NULL,
  expiration_time TIMESTAMP DEFAULT NULL,
  update_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`)
	require.NoError(t, err)

	for _, exec := range s.execs {
		_, err := s.GetConnection(t).ExecContext(ctx, exec)
		require.NoError(t, err)
	}
}

func (s *SQLite) Cleanup(t *testing.T) {}

// GetConnection returns the connection to the SQLite database.
func (s *SQLite) GetConnection(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", "file://"+s.dbPath+"?_txlock=immediate&_pragma=busy_timeout(5000)")
	require.NoError(t, err, "Failed to connect to SQLite database")
	t.Cleanup(func() {
		require.NoError(t, conn.Close())
	})
	return conn
}

// GetComponent returns the Component resource.
func (s *SQLite) GetComponent() string {
	return string(s.componentJSON)
}

func toDynamicValue(t *testing.T, val string) commonapi.DynamicValue {
	t.Helper()
	j, err := json.Marshal(val)
	require.NoError(t, err)
	return commonapi.DynamicValue{JSON: v1.JSON{Raw: j}}
}
