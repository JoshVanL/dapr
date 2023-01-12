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
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/injector/sidecar"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/placement/hashing"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/runtime/security"
	securityconsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement")

const gracefulTimeout = 10 * time.Second

func main() {
	log.Infof("starting Dapr Placement Service -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

	cfg := newConfig()

	// Apply options to all loggers.
	if err := logger.ApplyOptionsToLoggers(&cfg.loggerOptions); err != nil {
		log.Fatal(err)
	}
	log.Infof("log level set to: %s", cfg.loggerOptions.OutputLevel)

	// Initialize dapr metrics for placement.
	if err := cfg.metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}

	// Start Raft cluster.
	raftServer := raft.New(cfg.raftID, cfg.raftInMemEnabled, cfg.raftPeers, cfg.raftLogStorePath)
	if raftServer == nil {
		log.Fatal("failed to create raft server.")
	}

	if err := raftServer.StartRaft(nil); err != nil {
		log.Fatalf("failed to start Raft Server: %v", err)
	}

	// Start Placement gRPC server.
	hashing.SetReplicationFactor(cfg.replicationFactor)
	apiServer := placement.NewPlacementService(raftServer)

	trustAnchors, err := os.ReadFile(filepath.Join("/var/run/dapr/credentials", securityconsts.RootCertFilename))
	if err != nil {
		log.Fatalf("failed to read trust anchor: %s", err)
	}
	auth, err := security.NewAuthenticator(context.TODO(), security.Options{
		SentryAddress:     sidecar.ServiceAddressTODO(sidecar.ServiceSentry, os.Getenv("NAMESPACE")),
		SentryTrustDomain: cfg.sentryTrustDomain,
		TrustAnchors:      trustAnchors,
		Namespace:         os.Getenv("NAMESPACE"),
		AppID:             os.Getenv("NAMESPACE") + ":dapr-operator",
	})
	if err != nil {
		log.Fatalf("error creating authenticator: %s", err)
	}

	go apiServer.MonitorLeadership()
	go apiServer.Run(strconv.Itoa(cfg.placementPort), auth)
	log.Infof("placement service started on port %d", cfg.placementPort)

	// Start Healthz endpoint.
	go startHealthzServer(cfg.healthzPort)

	// Relay incoming process signal to exit placement gracefully
	signalCh := make(chan os.Signal, 10)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signalCh)

	<-signalCh

	// Shutdown servers
	gracefulExitCh := make(chan struct{})
	go func() {
		apiServer.Shutdown()
		raftServer.Shutdown()
		close(gracefulExitCh)
	}()

	select {
	case <-time.After(gracefulTimeout):
		log.Info("Timeout on graceful leave. Exiting...")
		os.Exit(1)

	case <-gracefulExitCh:
		log.Info("Gracefully exit.")
		os.Exit(0)
	}
}

func startHealthzServer(healthzPort int) {
	healthzServer := health.NewServer(log)
	healthzServer.Ready()

	if err := healthzServer.Run(context.Background(), healthzPort); err != nil {
		log.Fatalf("failed to start healthz server: %s", err)
	}
}
