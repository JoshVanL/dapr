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

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/placement/raft"
)

//nolint:gosec
const (
	defaultHealthzPort       = 8080
	defaultPlacementPort     = 50005
	defaultReplicationFactor = 100
)

type config struct {
	// Raft protocol configurations
	raftID           string
	raftPeerString   string
	raftPeers        []raft.PeerInfo
	raftInMemEnabled bool
	raftLogStorePath string

	// Placement server configurations
	placementPort int
	healthzPort   int
	tlsEnabled    bool

	replicationFactor int

	// Log and metrics configurations
	loggerOptions   logger.Options
	metricsExporter metrics.Exporter

	namespace        string
	trustDomain      string
	trustAnchorsFile string
	sentryHost       string
}

func newConfig() (*config, error) {
	// Default configuration
	cfg := config{
		raftID:           "dapr-placement-0",
		raftPeerString:   "dapr-placement-0=127.0.0.1:8201",
		raftPeers:        []raft.PeerInfo{},
		raftInMemEnabled: true,
		raftLogStorePath: "",

		placementPort: defaultPlacementPort,
		healthzPort:   defaultHealthzPort,
		tlsEnabled:    false,
		namespace:     os.Getenv("NAMESPACE"),
	}

	flag.StringVar(&cfg.raftID, "id", cfg.raftID, "Placement server ID.")
	flag.StringVar(&cfg.raftPeerString, "initial-cluster", cfg.raftPeerString, "raft cluster peers")
	flag.BoolVar(&cfg.raftInMemEnabled, "inmem-store-enabled", cfg.raftInMemEnabled, "Enable in-memory log and snapshot store unless --raft-logstore-path is set")
	flag.StringVar(&cfg.raftLogStorePath, "raft-logstore-path", cfg.raftLogStorePath, "raft log store path.")
	flag.IntVar(&cfg.placementPort, "port", cfg.placementPort, "sets the gRPC port for the placement service")
	flag.IntVar(&cfg.healthzPort, "healthz-port", cfg.healthzPort, "sets the HTTP port for the healthz server")
	flag.BoolVar(&cfg.tlsEnabled, "tls-enabled", cfg.tlsEnabled, "Should TLS be enabled for the placement gRPC server. Requires sentry server connection.")
	flag.IntVar(&cfg.replicationFactor, "replicationFactor", defaultReplicationFactor, "sets the replication factor for actor distribution on vnodes")

	flag.StringVar(&cfg.trustDomain, "trust-domain", "cluster.local", "Trust domain for the Dapr control plane")
	flag.StringVar(&cfg.trustAnchorsFile, "trust-anchors-file", "/var/run/secrets/dapr.io/tls/ca.crt", "Filepath to the trust anchors for the Dapr control plane")
	flag.StringVar(&cfg.sentryHost, "sentry-host", fmt.Sprintf("dapr-sentry.%s.svc", cfg.namespace), "Filepath to the trust anchors for the Dapr control plane")

	depCC := flag.String("certchain", "", "DEPRECATED")
	depRCF := flag.String("issuer-ca-filename", "", "DEPRECATED")
	depICF := flag.String("issuer-certificate-filename", "", "DEPRECATED")
	depIKF := flag.String("issuer-key-filename", "", "DEPRECATED")

	cfg.loggerOptions = logger.DefaultOptions()
	cfg.loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	cfg.metricsExporter = metrics.NewExporter(metrics.DefaultMetricNamespace)
	cfg.metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	if len(*depRCF) > 0 || len(*depICF) > 0 || len(*depIKF) > 0 || len(*depCC) > 0 {
		log.Warn("--certchain, --issuer-ca-filename, --issuer-certificate-filename and --issuer-key-filename are deprecated and will be removed in v1.12.")
	}

	cfg.raftPeers = parsePeersFromFlag(cfg.raftPeerString)
	if cfg.raftLogStorePath != "" {
		cfg.raftInMemEnabled = false
	}

	return &cfg, nil
}

func parsePeersFromFlag(val string) []raft.PeerInfo {
	peers := []raft.PeerInfo{}

	p := strings.Split(val, ",")
	for _, addr := range p {
		peer := strings.Split(addr, "=")
		if len(peer) != 2 {
			continue
		}

		peers = append(peers, raft.PeerInfo{
			ID:      strings.TrimSpace(peer[0]),
			Address: strings.TrimSpace(peer[1]),
		})
	}

	return peers
}
