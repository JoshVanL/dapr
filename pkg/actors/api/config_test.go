/*
Copyright 2024 The Dapr Authors
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

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/ptr"
)

func TestTranslateEntityConfig_Valid(t *testing.T) {
	appConfig := config.EntityConfig{
		Entities:                   []string{"ActorType1", "ActorType2"},
		ActorIdleTimeout:           "30s",
		DrainOngoingCallTimeout:    "20s",
		DrainRebalancedActors:      true,
		Reentrancy:                 config.ReentrancyConfig{Enabled: true, MaxStackDepth: ptr.Of(20)},
		RemindersStoragePartitions: 10,
	}

	domainConfig := TranslateEntityConfig(appConfig)

	assert.Len(t, domainConfig.Entities, 2)
	assert.Equal(t, 30*time.Second, domainConfig.ActorIdleTimeout)
	assert.Equal(t, 20*time.Second, domainConfig.DrainOngoingCallTimeout)
	assert.True(t, domainConfig.DrainRebalancedActors)
	assert.Equal(t, 10, domainConfig.RemindersStoragePartitions)
	assert.NotNil(t, domainConfig.ReentrancyConfig)
	assert.Equal(t, 20, *domainConfig.ReentrancyConfig.MaxStackDepth)
	assert.True(t, domainConfig.ReentrancyConfig.Enabled)
}

func TestTranslateEntityConfig_Defaults(t *testing.T) {
	appConfig := config.EntityConfig{
		Entities: []string{"ActorType1", "ActorType2"},
	}

	domainConfig := TranslateEntityConfig(appConfig)

	assert.Len(t, domainConfig.Entities, 2)
	assert.Equal(t, DefaultIdleTimeout, domainConfig.ActorIdleTimeout)
	assert.Equal(t, defaultOngoingCallTimeout, domainConfig.DrainOngoingCallTimeout)
	assert.False(t, domainConfig.DrainRebalancedActors)
	assert.Equal(t, 0, domainConfig.RemindersStoragePartitions)
	assert.NotNil(t, domainConfig.ReentrancyConfig)
	assert.False(t, domainConfig.ReentrancyConfig.Enabled)
	assert.Equal(t, DefaultReentrancyStackLimit, *domainConfig.ReentrancyConfig.MaxStackDepth)
}

func TestTranslateEntityConfig_InvalidDurations(t *testing.T) {
	// Given: App configuration with invalid durations
	appConfig := config.EntityConfig{
		Entities:                   []string{"ActorType1"},
		ActorIdleTimeout:           "invalid",
		DrainOngoingCallTimeout:    "invalid",
		DrainRebalancedActors:      true,
		Reentrancy:                 config.ReentrancyConfig{},
		RemindersStoragePartitions: 3,
	}

	// When: Translating the configuration
	domainConfig := TranslateEntityConfig(appConfig)

	// Then: Defaults are applied for invalid durations
	assert.Equal(t, DefaultIdleTimeout, domainConfig.ActorIdleTimeout)
	assert.Equal(t, defaultOngoingCallTimeout, domainConfig.DrainOngoingCallTimeout)
	assert.True(t, domainConfig.DrainRebalancedActors)
	assert.Equal(t, 3, domainConfig.RemindersStoragePartitions)
}

func TestTranslateEntityConfig_NilReentrancyStackDepth(t *testing.T) {
	// Given: App configuration with nil MaxStackDepth
	appConfig := config.EntityConfig{
		Entities:                   []string{"ActorType1"},
		ActorIdleTimeout:           "15m",
		DrainOngoingCallTimeout:    "30s",
		DrainRebalancedActors:      true,
		Reentrancy:                 config.ReentrancyConfig{MaxStackDepth: nil},
		RemindersStoragePartitions: 1,
	}

	// When: Translating the configuration
	domainConfig := TranslateEntityConfig(appConfig)

	// Then: Default MaxStackDepth is applied
	assert.Equal(t, 15*time.Minute, domainConfig.ActorIdleTimeout)
	assert.Equal(t, 30*time.Second, domainConfig.DrainOngoingCallTimeout)
	assert.True(t, domainConfig.DrainRebalancedActors)
	assert.NotNil(t, domainConfig.ReentrancyConfig.MaxStackDepth)
	assert.Equal(t, DefaultReentrancyStackLimit, *domainConfig.ReentrancyConfig.MaxStackDepth)
}
