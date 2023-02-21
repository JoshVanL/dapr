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
	"context"
	"fmt"
	"strconv"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/placement/hashing"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement")

func main() {
	log.Infof("Starting Dapr Placement Service -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

	cfg, err := newConfig()
	if err != nil {
		log.Fatal(err)
	}

	// Apply options to all loggers.
	if err := logger.ApplyOptionsToLoggers(&cfg.loggerOptions); err != nil {
		log.Fatal(err)
	}
	log.Infof("Log level set to: %s", cfg.loggerOptions.OutputLevel)

	// Initialize dapr metrics for placement.
	err = cfg.metricsExporter.Init()
	if err != nil {
		log.Fatal(err)
	}

	err = monitoring.InitMetrics()
	if err != nil {
		log.Fatal(err)
	}

	// Start Raft cluster.
	raftServer := raft.New(cfg.raftID, cfg.raftInMemEnabled, cfg.raftPeers, cfg.raftLogStorePath)
	if raftServer == nil {
		log.Fatal("Failed to create raft server.")
	}

	// Start Placement gRPC server.
	hashing.SetReplicationFactor(cfg.replicationFactor)
	apiServer := placement.NewPlacementService(raftServer)

	secProvider, err := security.New(security.Options{
		SentryAddress:           cfg.sentryAddress,
		ControlPlaneTrustDomain: cfg.trustDomain,
		ControlPlaneNamespace:   cfg.namespace,
		TrustAnchors:            cfg.trustAnchors,
		AppID:                   "dapr-placement",
		AppNamespace:            cfg.namespace,
		MTLSEnabled:             cfg.tlsEnabled,
	})
	if err != nil {
		log.Fatal(err)
	}

	err = concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			return raftServer.StartRaft(ctx, nil)
		},
		apiServer.MonitorLeadership,
		func(ctx context.Context) error {
			healthzServer := health.NewServer(log)
			healthzServer.Ready()
			if healthzErr := healthzServer.Run(ctx, cfg.healthzPort); healthzErr != nil {
				return fmt.Errorf("failed to start healthz server: %w", healthzErr)
			}
			return nil
		},
		secProvider.Start,
		func(ctx context.Context) error {
			sec, err := secProvider.Security(ctx)
			if err != nil {
				return err
			}
			return apiServer.Run(ctx, strconv.Itoa(cfg.placementPort), sec)
		},
	).Run(signals.Context())
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Placement service shut down gracefully")
}
