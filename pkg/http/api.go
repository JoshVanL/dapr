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

package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/mitchellh/mapstructure"
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/components-contrib/lock"
	contribMetadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	wfs "github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/actors"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/utils"
)

// API returns a list of HTTP endpoints for Dapr.
type API interface {
	APIEndpoints() []Endpoint
	PublicEndpoints() []Endpoint
	MarkStatusAsReady()
	MarkStatusAsOutboundReady()
	SetAppChannel(appChannel channel.AppChannel)
	SetDirectMessaging(directMessaging messaging.DirectMessaging)
	SetActorRuntime(actor actors.Actors)
}

type api struct {
	endpoints                  []Endpoint
	publicEndpoints            []Endpoint
	directMessaging            messaging.DirectMessaging
	appChannel                 channel.AppChannel
	getComponentsFn            func(context.Context) []componentsV1alpha1.Component
	getSubscriptionsFn         func(context.Context) ([]runtimePubsub.Subscription, error)
	resiliency                 resiliency.Provider
	stateStores                map[string]state.Store
	workflowComponents         map[string]wfs.Workflow
	lockStores                 map[string]lock.Store
	configurationStores        map[string]configuration.Store
	configurationSubscribe     map[string]chan struct{}
	transactionalStateStores   map[string]state.TransactionalStore
	secretStores               map[string]secretstores.SecretStore
	secretsConfiguration       map[string]config.SecretsScope
	actor                      actors.Actors
	pubsubAdapter              runtimePubsub.Adapter
	sendToOutputBindingFn      func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	id                         string
	extendedMetadata           sync.Map
	readyStatus                bool
	outboundReadyStatus        bool
	tracingSpec                config.TracingSpec
	shutdown                   func()
	getComponentsCapabilitesFn func(context.Context) map[string][]string
	daprRunTimeVersion         string
	maxRequestBodySize         int64 // In bytes
}

type registeredComponent struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

type pubsubSubscription struct {
	PubsubName      string                    `json:"pubsubname"`
	Topic           string                    `json:"topic"`
	DeadLetterTopic string                    `json:"deadLetterTopic"`
	Metadata        map[string]string         `json:"metadata"`
	Rules           []*pubsubSubscriptionRule `json:"rules,omitempty"`
}

type pubsubSubscriptionRule struct {
	Match string `json:"match"`
	Path  string `json:"path"`
}

type metadata struct {
	ID                   string                     `json:"id"`
	ActiveActorsCount    []actors.ActiveActorsCount `json:"actors"`
	Extended             map[string]string          `json:"extended"`
	RegisteredComponents []registeredComponent      `json:"components"`
	Subscriptions        []pubsubSubscription       `json:"subscriptions"`
}

const (
	apiVersionV1             = "v1.0"
	apiVersionV1alpha1       = "v1.0-alpha1"
	idParam                  = "id"
	methodParam              = "method"
	topicParam               = "topic"
	actorTypeParam           = "actorType"
	actorIDParam             = "actorId"
	storeNameParam           = "storeName"
	stateKeyParam            = "key"
	configurationKeyParam    = "key"
	configurationSubscribeID = "configurationSubscribeID"
	secretStoreNameParam     = "secretStoreName"
	secretNameParam          = "key"
	nameParam                = "name"
	workflowComponent        = "workflowComponent"
	workflowType             = "workflowType"
	instanceID               = "instanceID"
	consistencyParam         = "consistency"
	concurrencyParam         = "concurrency"
	pubsubnameparam          = "pubsubname"
	traceparentHeader        = "traceparent"
	tracestateHeader         = "tracestate"
	daprAppID                = "dapr-app-id"
	daprRuntimeVersionKey    = "daprRuntimeVersion"
)

// APIOpts contains the options for NewAPI.
type APIOpts struct {
	AppID                       string
	AppChannel                  channel.AppChannel
	DirectMessaging             messaging.DirectMessaging
	GetComponentsFn             func(context.Context) []componentsV1alpha1.Component
	GetSubscriptionsFn          func(context.Context) ([]runtimePubsub.Subscription, error)
	Resiliency                  resiliency.Provider
	StateStores                 map[string]state.Store
	WorkflowsComponents         map[string]wfs.Workflow
	LockStores                  map[string]lock.Store
	SecretStores                map[string]secretstores.SecretStore
	SecretsConfiguration        map[string]config.SecretsScope
	ConfigurationStores         map[string]configuration.Store
	PubsubAdapter               runtimePubsub.Adapter
	Actor                       actors.Actors
	SendToOutputBindingFn       func(ctx context.Context, name string, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error)
	TracingSpec                 config.TracingSpec
	Shutdown                    func()
	GetComponentsCapabilitiesFn func(context.Context) map[string][]string
	MaxRequestBodySize          int64 // In bytes
}

// NewAPI returns a new API.
func NewAPI(ctx context.Context, opts APIOpts) API {
	transactionalStateStores := map[string]state.TransactionalStore{}
	for key, store := range opts.StateStores {
		if state.FeatureTransactional.IsPresent(store.Features(ctx)) {
			transactionalStateStores[key] = store.(state.TransactionalStore)
		}
	}
	api := &api{
		id:                         opts.AppID,
		appChannel:                 opts.AppChannel,
		directMessaging:            opts.DirectMessaging,
		getComponentsFn:            opts.GetComponentsFn,
		getSubscriptionsFn:         opts.GetSubscriptionsFn,
		resiliency:                 opts.Resiliency,
		stateStores:                opts.StateStores,
		workflowComponents:         opts.WorkflowsComponents,
		lockStores:                 opts.LockStores,
		secretStores:               opts.SecretStores,
		secretsConfiguration:       opts.SecretsConfiguration,
		configurationStores:        opts.ConfigurationStores,
		pubsubAdapter:              opts.PubsubAdapter,
		actor:                      opts.Actor,
		sendToOutputBindingFn:      opts.SendToOutputBindingFn,
		tracingSpec:                opts.TracingSpec,
		shutdown:                   opts.Shutdown,
		getComponentsCapabilitesFn: opts.GetComponentsCapabilitiesFn,
		maxRequestBodySize:         opts.MaxRequestBodySize,
		transactionalStateStores:   transactionalStateStores,
		configurationSubscribe:     make(map[string]chan struct{}),
		daprRunTimeVersion:         buildinfo.Version(),
	}

	metadataEndpoints := api.constructMetadataEndpoints()
	healthEndpoints := api.constructHealthzEndpoints()

	api.endpoints = append(api.endpoints, api.constructStateEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructSecretEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructPubSubEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructActorEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructDirectMessagingEndpoints()...)
	api.endpoints = append(api.endpoints, metadataEndpoints...)
	api.endpoints = append(api.endpoints, api.constructShutdownEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructBindingsEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructConfigurationEndpoints()...)
	api.endpoints = append(api.endpoints, healthEndpoints...)
	api.endpoints = append(api.endpoints, api.constructDistributedLockEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructWorkflowEndpoints()...)

	api.publicEndpoints = append(api.publicEndpoints, metadataEndpoints...)
	api.publicEndpoints = append(api.publicEndpoints, healthEndpoints...)

	return api
}

// APIEndpoints returns the list of registered endpoints.
func (a *api) APIEndpoints() []Endpoint {
	return a.endpoints
}

// PublicEndpoints returns the list of registered endpoints.
func (a *api) PublicEndpoints() []Endpoint {
	return a.publicEndpoints
}

// MarkStatusAsReady marks the ready status of dapr.
func (a *api) MarkStatusAsReady() {
	a.readyStatus = true
}

// MarkStatusAsOutboundReady marks the ready status of dapr for outbound traffic.
func (a *api) MarkStatusAsOutboundReady() {
	a.outboundReadyStatus = true
}

// Workflow Component: Component specified in yaml (temporal, etc..)
// Workflow Type: Name of the workflow to run (function name)
// Instance ID: Identifier of the specific run
func (a *api) constructWorkflowEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "workflows/{workflowComponent}/{workflowType}/{instanceID}",
			Version: apiVersionV1alpha1,
			Handler: a.onGetWorkflow,
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "workflows/{workflowComponent}/{workflowType}/{instanceID}/start",
			Version: apiVersionV1alpha1,
			Handler: a.onStartWorkflow,
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/terminate",
			Version: apiVersionV1alpha1,
			Handler: a.onTerminateWorkflow,
		},
	}
}

func (a *api) constructStateEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetState,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "state/{storeName}",
			Version: apiVersionV1,
			Handler: a.onPostState,
		},
		{
			Methods: []string{fasthttp.MethodDelete},
			Route:   "state/{storeName}/{key}",
			Version: apiVersionV1,
			Handler: a.onDeleteState,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "state/{storeName}/bulk",
			Version: apiVersionV1,
			Handler: a.onBulkGetState,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "state/{storeName}/transaction",
			Version: apiVersionV1,
			Handler: a.onPostStateTransaction,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "state/{storeName}/query",
			Version: apiVersionV1alpha1,
			Handler: a.onQueryState,
		},
	}
}

func (a *api) constructSecretEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "secrets/{secretStoreName}/bulk",
			Version: apiVersionV1,
			Handler: a.onBulkGetSecret,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "secrets/{secretStoreName}/{key}",
			Version: apiVersionV1,
			Handler: a.onGetSecret,
		},
	}
}

func (a *api) constructPubSubEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "publish/{pubsubname}/{topic:*}",
			Version: apiVersionV1,
			Handler: a.onPublish,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "publish/bulk/{pubsubname}/{topic:*}",
			Version: apiVersionV1alpha1,
			Handler: a.onBulkPublish,
		},
	}
}

func (a *api) constructBindingsEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "bindings/{name}",
			Version: apiVersionV1,
			Handler: a.onOutputBindingMessage,
		},
	}
}

func (a *api) constructDirectMessagingEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:           []string{router.MethodWild},
			Route:             "invoke/{id}/method/{method:*}",
			Alias:             "{method:*}",
			Version:           apiVersionV1,
			KeepParamUnescape: true,
			Handler:           a.onDirectMessage,
		},
	}
}

func (a *api) constructActorEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/state",
			Version: apiVersionV1,
			Handler: a.onActorStateTransaction,
		},
		{
			Methods: []string{fasthttp.MethodGet, fasthttp.MethodPost, fasthttp.MethodDelete, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/method/{method}",
			Version: apiVersionV1,
			Handler: a.onDirectActorMessage,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "actors/{actorType}/{actorId}/state/{key}",
			Version: apiVersionV1,
			Handler: a.onGetActorState,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onCreateActorReminder,
		},
		{
			Methods: []string{fasthttp.MethodPost, fasthttp.MethodPut},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Handler: a.onCreateActorTimer,
		},
		{
			Methods: []string{fasthttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorReminder,
		},
		{
			Methods: []string{fasthttp.MethodDelete},
			Route:   "actors/{actorType}/{actorId}/timers/{name}",
			Version: apiVersionV1,
			Handler: a.onDeleteActorTimer,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onGetActorReminder,
		},
		{
			Methods: []string{fasthttp.MethodPatch},
			Route:   "actors/{actorType}/{actorId}/reminders/{name}",
			Version: apiVersionV1,
			Handler: a.onRenameActorReminder,
		},
	}
}

func (a *api) constructMetadataEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "metadata",
			Version: apiVersionV1,
			Handler: a.onGetMetadata,
		},
		{
			Methods: []string{fasthttp.MethodPut},
			Route:   "metadata/{key}",
			Version: apiVersionV1,
			Handler: a.onPutMetadata,
		},
	}
}

func (a *api) constructShutdownEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "shutdown",
			Version: apiVersionV1,
			Handler: a.onShutdown,
		},
	}
}

func (a *api) constructHealthzEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods:       []string{fasthttp.MethodGet},
			Route:         "healthz",
			Version:       apiVersionV1,
			Handler:       a.onGetHealthz,
			AlwaysAllowed: true,
			IsHealthCheck: true,
		},
		{
			Methods:       []string{fasthttp.MethodGet},
			Route:         "healthz/outbound",
			Version:       apiVersionV1,
			Handler:       a.onGetOutboundHealthz,
			AlwaysAllowed: true,
			IsHealthCheck: true,
		},
	}
}

func (a *api) constructConfigurationEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "configuration/{storeName}",
			Version: apiVersionV1alpha1,
			Handler: a.onGetConfiguration,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "configuration/{storeName}/subscribe",
			Version: apiVersionV1alpha1,
			Handler: a.onSubscribeConfiguration,
		},
		{
			Methods: []string{fasthttp.MethodGet},
			Route:   "configuration/{storeName}/{configurationSubscribeID}/unsubscribe",
			Version: apiVersionV1alpha1,
			Handler: a.onUnsubscribeConfiguration,
		},
	}
}

func (a *api) constructDistributedLockEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "lock/{storeName}",
			Version: apiVersionV1alpha1,
			Handler: a.onLock,
		},
		{
			Methods: []string{fasthttp.MethodPost},
			Route:   "unlock/{storeName}",
			Version: apiVersionV1alpha1,
			Handler: a.onUnlock,
		},
	}
}

func (a *api) onOutputBindingMessage(ctx *fasthttp.RequestCtx) {
	name := ctx.UserValue(nameParam).(string)
	body := ctx.PostBody()

	var req OutputBindingRequest
	err := json.Unmarshal(body, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	b, err := json.Marshal(req.Data)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST_DATA", fmt.Sprintf(messages.ErrMalformedRequestData, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	// pass the trace context to output binding in metadata
	if span := diagUtils.SpanFromContext(ctx); span != nil {
		sc := span.SpanContext()
		if req.Metadata == nil {
			req.Metadata = map[string]string{}
		}
		// if sc is not empty context, set traceparent Header.
		if !sc.Equal(trace.SpanContext{}) {
			req.Metadata[traceparentHeader] = diag.SpanContextToW3CString(sc)
		}
		if sc.TraceState().Len() == 0 {
			req.Metadata[tracestateHeader] = diag.TraceStateToW3CString(sc)
		}
	}

	start := time.Now()
	resp, err := a.sendToOutputBindingFn(ctx, name, &bindings.InvokeRequest{
		Metadata:  req.Metadata,
		Data:      b,
		Operation: bindings.OperationKind(req.Operation),
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.OutputBindingEvent(ctx, name, req.Operation, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf(messages.ErrInvokeOutputBinding, name, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil {
		respond(ctx, withEmpty())
	} else {
		respond(ctx, withMetadata(resp.Metadata), withJSON(fasthttp.StatusOK, resp.Data))
	}
}

type bulkGetRes struct {
	bulkGet   bool
	responses []state.BulkGetResponse
}

func (a *api) onBulkGetState(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	var req BulkGetRequest
	err = json.Unmarshal(ctx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromRequest(ctx)
	if req.Metadata == nil {
		req.Metadata = metadata
	} else {
		for k, v := range metadata {
			req.Metadata[k] = v
		}
	}

	bulkResp := make([]BulkGetResponse, len(req.Keys))
	if len(req.Keys) == 0 {
		b, _ := json.Marshal(bulkResp)
		respond(ctx, withJSON(fasthttp.StatusOK, b))
		return
	}

	// try bulk get first
	reqs := make([]state.GetRequest, len(req.Keys))
	for i, k := range req.Keys {
		key, err1 := stateLoader.GetModifiedStateKey(k, storeName, a.id)
		if err1 != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err1))
			respond(ctx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(err1)
			return
		}
		r := state.GetRequest{
			Key:      key,
			Metadata: req.Metadata,
		}
		reqs[i] = r
	}

	start := time.Now()
	policyDef := a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Statestore)
	bgrPolicyRunner := resiliency.NewRunner[*bulkGetRes](ctx, policyDef)
	bgr, rErr := bgrPolicyRunner(func(ctx context.Context) (*bulkGetRes, error) {
		rBulkGet, rBulkResponse, rErr := store.BulkGet(ctx, reqs)
		return &bulkGetRes{
			bulkGet:   rBulkGet,
			responses: rBulkResponse,
		}, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, storeName, diag.BulkGet, err == nil, elapsed)

	if bgr == nil {
		bgr = &bulkGetRes{}
	}

	if bgr.bulkGet {
		// if store supports bulk get
		if rErr != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
			respond(ctx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}

		for i := 0; i < len(bgr.responses) && i < len(req.Keys); i++ {
			bulkResp[i].Key = stateLoader.GetOriginalStateKey(bgr.responses[i].Key)
			if bgr.responses[i].Error != "" {
				log.Debugf("bulk get: error getting key %s: %s", bulkResp[i].Key, bgr.responses[i].Error)
				bulkResp[i].Error = bgr.responses[i].Error
			} else {
				bulkResp[i].Data = json.RawMessage(bgr.responses[i].Data)
				bulkResp[i].ETag = bgr.responses[i].ETag
				bulkResp[i].Metadata = bgr.responses[i].Metadata
			}
		}
	} else {
		// if store doesn't support bulk get, fallback to call get() method one by one
		limiter := concurrency.NewLimiter(req.Parallelism)

		for i, k := range req.Keys {
			bulkResp[i].Key = k

			fn := func(param interface{}) {
				r := param.(*BulkGetResponse)
				k, err := stateLoader.GetModifiedStateKey(r.Key, storeName, a.id)
				if err != nil {
					log.Debug(err)
					r.Error = err.Error()
					return
				}
				gr := &state.GetRequest{
					Key:      k,
					Metadata: metadata,
				}

				policyRunner := resiliency.NewRunner[*state.GetResponse](ctx, policyDef)
				resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
					return store.Get(ctx, gr)
				})
				if err != nil {
					log.Debugf("bulk get: error getting key %s: %s", r.Key, err)
					r.Error = err.Error()
				} else if resp != nil {
					r.Data = json.RawMessage(resp.Data)
					r.ETag = resp.ETag
					r.Metadata = resp.Metadata
				}
			}

			limiter.Execute(fn, &bulkResp[i])
		}
		limiter.Wait()
	}

	if encryption.EncryptedStateStore(storeName) {
		for i := range bulkResp {
			val, err := encryption.TryDecryptValue(storeName, bulkResp[i].Data)
			if err != nil {
				log.Debugf("bulk get error: %s", err)
				bulkResp[i].Error = err.Error()
				continue
			}

			bulkResp[i].Data = val
		}
	}

	b, _ := json.Marshal(bulkResp)
	respond(ctx, withJSON(fasthttp.StatusOK, b))
}

func (a *api) getStateStoreWithRequestValidation(ctx *fasthttp.RequestCtx) (state.Store, string, error) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_CONFIGURED", messages.ErrStateStoresNotConfigured)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(ctx)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrStateStoreNotFound, storeName))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}
	return a.stateStores[storeName], storeName, nil
}

func (a *api) getLockStoreWithRequestValidation(ctx *fasthttp.RequestCtx) (lock.Store, string, error) {
	if a.lockStores == nil || len(a.lockStores) == 0 {
		msg := NewErrorResponse("ERR_LOCK_STORE_NOT_CONFIGURED", messages.ErrLockStoresNotConfigured)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(ctx)

	if a.lockStores[storeName] == nil {
		msg := NewErrorResponse("ERR_LOCK_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrLockStoreNotFound, storeName))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}
	return a.lockStores[storeName], storeName, nil
}

// Route:   "workflows/{workflowComponent}/{workflowType}/{instanceId}",
// Workflow Component: Component specified in yaml (temporal, etc..)
// Workflow Type: Name of the workflow to run (function name)
// Instance ID: Identifier of the specific run
func (a *api) onStartWorkflow(ctx *fasthttp.RequestCtx) {
	startReq := wfs.StartRequest{}

	wfType := ctx.UserValue(workflowType).(string)
	if wfType == "" {
		msg := NewErrorResponse("ERR_NO_WORKFLOW_TYPE_PROVIDED", messages.ErrWorkflowNameMissing)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	component := ctx.UserValue(workflowComponent).(string)
	if component == "" {
		msg := NewErrorResponse("ERR_NO_WORKFLOW_COMPONENT_PROVIDED", messages.ErrNoOrMissingWorkflowComponent)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	workflowRun := a.workflowComponents[component]
	if workflowRun == nil {
		msg := NewErrorResponse("ERR_NON_EXISTENT_WORKFLOW_COMPONENT_PROVIDED", fmt.Sprintf(messages.ErWorkflowrComponentDoesNotExist, component))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	err := json.Unmarshal(ctx.PostBody(), &startReq)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	instance := ctx.UserValue(instanceID).(string)
	if instance == "" {
		msg := NewErrorResponse("ERR_NO_INSTANCE_ID_PROVIDED", fmt.Sprintf(messages.ErrMissingOrEmptyInstance))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req := wfs.StartRequest{
		Options:           startReq.Options,
		WorkflowReference: startReq.WorkflowReference,
		WorkflowName:      wfType,
		Input:             startReq.Input,
	}
	req.WorkflowReference.InstanceID = instance

	resp, err := workflowRun.Start(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_START_WORKFLOW", fmt.Sprintf(messages.ErrStartWorkflow, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	response, err := json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", fmt.Sprintf(messages.ErrMetadataGet, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	log.Debug(resp)
	respond(ctx, withJSON(fasthttp.StatusAccepted, response))
}

func (a *api) onGetWorkflow(ctx *fasthttp.RequestCtx) {
	wfType := ctx.UserValue(workflowType).(string)
	if wfType == "" {
		msg := NewErrorResponse("ERR_NO_WORKFLOW_TYPE_PROVIDED", messages.ErrWorkflowNameMissing)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	component := ctx.UserValue(workflowComponent).(string)
	if component == "" {
		msg := NewErrorResponse("ERR_NO_WORKFLOW_COMPONENT_PROVIDED", messages.ErrNoOrMissingWorkflowComponent)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	instance := ctx.UserValue(instanceID).(string)
	if instance == "" {
		msg := NewErrorResponse("ERR_NO_INSTANCE_ID_PROVIDED", messages.ErrMissingOrEmptyInstance)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	workflowRun := a.workflowComponents[component]
	if workflowRun == nil {
		msg := NewErrorResponse("ERR_NON_EXISTENT_WORKFLOW_COMPONENT_PROVIDED", fmt.Sprintf(messages.ErWorkflowrComponentDoesNotExist, component))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req := wfs.WorkflowReference{
		InstanceID: instance,
	}

	resp, err := workflowRun.Get(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_GET_WORKFLOW", fmt.Sprintf(messages.ErrWorkflowGetResponse, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	response, err := json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", fmt.Sprintf(messages.ErrMetadataGet, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	respond(ctx, withJSON(fasthttp.StatusAccepted, response))
}

func (a *api) onTerminateWorkflow(ctx *fasthttp.RequestCtx) {
	instance := ctx.UserValue(instanceID).(string)
	if instance == "" {
		msg := NewErrorResponse("ERR_NO_INSTANCE_ID_PROVIDED", messages.ErrMissingOrEmptyInstance)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	component := ctx.UserValue(workflowComponent).(string)
	if component == "" {
		msg := NewErrorResponse("ERR_NO_WORKFLOW_COMPONENT_PROVIDED", messages.ErrNoOrMissingWorkflowComponent)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	workflowRun := a.workflowComponents[component]
	if workflowRun == nil {
		msg := NewErrorResponse("ERR_NON_EXISTENT_WORKFLOW_COMPONENT_PROVIDED", fmt.Sprintf(messages.ErWorkflowrComponentDoesNotExist, component))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req := wfs.WorkflowReference{
		InstanceID: instance,
	}

	err := workflowRun.Terminate(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_TERMINATE_WORKFLOW", fmt.Sprintf(messages.ErrTerminateWorkflow, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
}

func (a *api) onGetState(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(ctx)

	key := ctx.UserValue(stateKeyParam).(string)
	consistency := string(ctx.QueryArgs().Peek(consistencyParam))
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(err)
		return
	}
	req := &state.GetRequest{
		Key: k,
		Options: state.GetStateOption{
			Consistency: consistency,
		},
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*state.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Statestore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*state.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil || resp.Data == nil {
		respond(ctx, withEmpty())
		return
	}

	if encryption.EncryptedStateStore(storeName) {
		val, err := encryption.TryDecryptValue(storeName, resp.Data)
		if err != nil {
			msg := NewErrorResponse("ERR_STATE_GET", fmt.Sprintf(messages.ErrStateGet, key, storeName, err.Error()))
			respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}

		resp.Data = val
	}

	respond(ctx, withJSON(fasthttp.StatusOK, resp.Data), withEtag(resp.ETag), withMetadata(resp.Metadata))
}

func (a *api) getConfigurationStoreWithRequestValidation(ctx *fasthttp.RequestCtx) (configuration.Store, string, error) {
	if a.configurationStores == nil || len(a.configurationStores) == 0 {
		msg := NewErrorResponse("ERR_CONFIGURATION_STORE_NOT_CONFIGURED", messages.ErrConfigurationStoresNotConfigured)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}

	storeName := a.getStateStoreName(ctx)

	if a.configurationStores[storeName] == nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrConfigurationStoreNotFound, storeName))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return nil, "", errors.New(msg.Message)
	}
	return a.configurationStores[storeName], storeName, nil
}

type subscribeConfigurationResponse struct {
	ID string `json:"id"`
}

type UnsubscribeConfigurationResponse struct {
	Ok      bool   `protobuf:"varint,1,opt,name=ok,proto3" json:"ok,omitempty"`
	Message string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
}

type configurationEventHandler struct {
	api        *api
	storeName  string
	appChannel channel.AppChannel
	res        resiliency.Provider
}

func (h *configurationEventHandler) updateEventHandler(ctx context.Context, e *configuration.UpdateEvent) error {
	if h.appChannel == nil {
		err := fmt.Errorf("app channel is nil. unable to send configuration update from %s", h.storeName)
		log.Error(err)
		return err
	}
	for key := range e.Items {
		policyDef := h.res.ComponentInboundPolicy(ctx, h.storeName, resiliency.Configuration)

		eventBody := &bytes.Buffer{}
		_ = json.NewEncoder(eventBody).Encode(e)

		req := invokev1.NewInvokeMethodRequest("/configuration/"+h.storeName+"/"+key).
			WithHTTPExtension(nethttp.MethodPost, "").
			WithRawData(eventBody).
			WithContentType(invokev1.JSONContentType)
		if policyDef != nil {
			req.WithReplay(policyDef.HasRetries())
		}
		defer req.Close()

		policyRunner := resiliency.NewRunner[any](ctx, policyDef)
		_, err := policyRunner(func(ctx context.Context) (any, error) {
			rResp, rErr := h.appChannel.InvokeMethod(ctx, req)
			if rErr != nil {
				return nil, rErr
			}
			if rResp != nil {
				defer rResp.Close()
			}

			if rResp != nil && rResp.Status().Code != nethttp.StatusOK {
				return nil, fmt.Errorf("error sending configuration item to application, status %d", rResp.Status().Code)
			}
			return nil, nil
		})
		if err != nil {
			log.Errorf("error sending configuration item to the app: %v", err)
		}
	}
	return nil
}

func (a *api) onLock(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getLockStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	req := lock.TryLockRequest{}
	err = json.Unmarshal(ctx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.ResourceID, err = lockLoader.GetModifiedLockKey(req.ResourceID, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_TRY_LOCK", err.Error())
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	policyRunner := resiliency.NewRunner[*lock.TryLockResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Lock),
	)
	resp, err := policyRunner(func(ctx context.Context) (*lock.TryLockResponse, error) {
		return store.TryLock(ctx, &req)
	})
	if err != nil {
		msg := NewErrorResponse("ERR_TRY_LOCK", err.Error())
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	b, _ := json.Marshal(resp)
	respond(ctx, withJSON(200, b))
}

func (a *api) onUnlock(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getLockStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	req := &lock.UnlockRequest{}
	err = json.Unmarshal(ctx.PostBody(), req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.ResourceID, err = lockLoader.GetModifiedLockKey(req.ResourceID, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_UNLOCK", err.Error())
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	policyRunner := resiliency.NewRunner[*lock.UnlockResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Lock),
	)
	resp, err := policyRunner(func(ctx context.Context) (*lock.UnlockResponse, error) {
		return store.Unlock(ctx, req)
	})
	if err != nil {
		msg := NewErrorResponse("ERR_UNLOCK", err.Error())
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	b, _ := json.Marshal(resp)
	respond(ctx, withJSON(200, b))
}

func (a *api) onSubscribeConfiguration(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}
	if a.appChannel == nil {
		msg := NewErrorResponse("ERR_APP_CHANNEL_NIL", "app channel is not initialized. cannot subscribe to configuration updates")
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	metadata := getMetadataFromRequest(ctx)
	subscribeKeys := make([]string, 0)

	keys := make([]string, 0)
	queryKeys := ctx.QueryArgs().PeekMulti(configurationKeyParam)
	for _, queryKeyByte := range queryKeys {
		keys = append(keys, string(queryKeyByte))
	}

	if len(keys) > 0 {
		subscribeKeys = append(subscribeKeys, keys...)
	}

	req := &configuration.SubscribeRequest{
		Keys:     subscribeKeys,
		Metadata: metadata,
	}

	// create handler
	handler := &configurationEventHandler{
		api:        a,
		storeName:  storeName,
		appChannel: a.appChannel,
		res:        a.resiliency,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[string](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Configuration),
	)
	subscribeID, err := policyRunner(func(ctx context.Context) (string, error) {
		return store.Subscribe(ctx, req, handler.updateEventHandler)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(ctx, storeName, diag.ConfigurationSubscribe, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_SUBSCRIBE", fmt.Sprintf(messages.ErrConfigurationSubscribe, keys, storeName, err.Error()))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&subscribeConfigurationResponse{
		ID: subscribeID,
	})
	respond(ctx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) onUnsubscribeConfiguration(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}
	subscribeID := ctx.UserValue(configurationSubscribeID).(string)

	req := configuration.UnsubscribeRequest{
		ID: subscribeID,
	}
	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Configuration),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Unsubscribe(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.ConfigurationInvoked(ctx, storeName, diag.ConfigurationUnsubscribe, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_UNSUBSCRIBE", fmt.Sprintf(messages.ErrConfigurationUnsubscribe, subscribeID, err.Error()))
		errRespBytes, _ := json.Marshal(&UnsubscribeConfigurationResponse{
			Ok:      false,
			Message: msg.Message,
		})
		respond(ctx, withJSON(fasthttp.StatusInternalServerError, errRespBytes))
		log.Debug(msg)
		return
	}
	respBytes, _ := json.Marshal(&UnsubscribeConfigurationResponse{
		Ok: true,
	})
	respond(ctx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) onGetConfiguration(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getConfigurationStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(ctx)

	keys := make([]string, 0)
	queryKeys := ctx.QueryArgs().PeekMulti(configurationKeyParam)
	for _, queryKeyByte := range queryKeys {
		keys = append(keys, string(queryKeyByte))
	}
	req := &configuration.GetRequest{
		Keys:     keys,
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*configuration.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Configuration),
	)
	getResponse, err := policyRunner(func(ctx context.Context) (*configuration.GetResponse, error) {
		return store.Get(ctx, req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.ConfigurationInvoked(ctx, storeName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_CONFIGURATION_GET", fmt.Sprintf(messages.ErrConfigurationGet, keys, storeName, err.Error()))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if getResponse == nil || getResponse.Items == nil || len(getResponse.Items) == 0 {
		respond(ctx, withEmpty())
		return
	}

	respBytes, _ := json.Marshal(getResponse.Items)

	respond(ctx, withJSON(fasthttp.StatusOK, respBytes))
}

func extractEtag(ctx *fasthttp.RequestCtx) (hasEtag bool, etag string) {
	ctx.Request.Header.VisitAll(func(key []byte, value []byte) {
		if string(key) == "If-Match" {
			etag = string(value)
			hasEtag = true
			return
		}
	})

	return hasEtag, etag
}

func (a *api) onDeleteState(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	key := ctx.UserValue(stateKeyParam).(string)

	concurrency := string(ctx.QueryArgs().Peek(concurrencyParam))
	consistency := string(ctx.QueryArgs().Peek(consistencyParam))

	metadata := getMetadataFromRequest(ctx)
	k, err := stateLoader.GetModifiedStateKey(key, storeName, a.id)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(err)
		return
	}
	req := state.DeleteRequest{
		Key: k,
		Options: state.DeleteStateOption{
			Concurrency: concurrency,
			Consistency: consistency,
		},
		Metadata: metadata,
	}

	exists, etag := extractEtag(ctx)
	if exists {
		req.ETag = &etag
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.Delete(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, storeName, diag.Delete, err == nil, elapsed)

	if err != nil {
		statusCode, errMsg, resp := a.stateErrorResponse(err, "ERR_STATE_DELETE")
		resp.Message = fmt.Sprintf(messages.ErrStateDelete, key, errMsg)

		respond(ctx, withError(statusCode, resp))
		log.Debug(resp.Message)
		return
	}
	respond(ctx, withEmpty())
}

func (a *api) onGetSecret(ctx *fasthttp.RequestCtx) {
	store, secretStoreName, err := a.getSecretStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(ctx)

	key := ctx.UserValue(secretNameParam).(string)

	if !a.isSecretAllowed(secretStoreName, key) {
		msg := NewErrorResponse("ERR_PERMISSION_DENIED", fmt.Sprintf(messages.ErrPermissionDenied, key, secretStoreName))
		respond(ctx, withError(fasthttp.StatusForbidden, msg))
		return
	}

	req := secretstores.GetSecretRequest{
		Name:     key,
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*secretstores.GetSecretResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, secretStoreName, resiliency.Secretstore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*secretstores.GetSecretResponse, error) {
		rResp, rErr := store.GetSecret(ctx, req)
		return &rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(ctx, secretStoreName, diag.Get, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_SECRET_GET",
			fmt.Sprintf(messages.ErrSecretGet, req.Name, secretStoreName, err.Error()))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil || resp.Data == nil {
		respond(ctx, withEmpty())
		return
	}

	respBytes, _ := json.Marshal(resp.Data)
	respond(ctx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) onBulkGetSecret(ctx *fasthttp.RequestCtx) {
	store, secretStoreName, err := a.getSecretStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	metadata := getMetadataFromRequest(ctx)

	req := secretstores.BulkGetSecretRequest{
		Metadata: metadata,
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*secretstores.BulkGetSecretResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, secretStoreName, resiliency.Secretstore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*secretstores.BulkGetSecretResponse, error) {
		rResp, rErr := store.BulkGetSecret(ctx, req)
		return &rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.SecretInvoked(ctx, secretStoreName, diag.BulkGet, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_SECRET_GET",
			fmt.Sprintf(messages.ErrBulkSecretGet, secretStoreName, err.Error()))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil || resp.Data == nil {
		respond(ctx, withEmpty())
		return
	}

	filteredSecrets := map[string]map[string]string{}
	for key, v := range resp.Data {
		if a.isSecretAllowed(secretStoreName, key) {
			filteredSecrets[key] = v
		} else {
			log.Debugf(messages.ErrPermissionDenied, key, secretStoreName)
		}
	}

	respBytes, _ := json.Marshal(filteredSecrets)
	respond(ctx, withJSON(fasthttp.StatusOK, respBytes))
}

func (a *api) getSecretStoreWithRequestValidation(ctx *fasthttp.RequestCtx) (secretstores.SecretStore, string, error) {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		msg := NewErrorResponse("ERR_SECRET_STORES_NOT_CONFIGURED", messages.ErrSecretStoreNotConfigured)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		return nil, "", errors.New(msg.Message)
	}

	secretStoreName := ctx.UserValue(secretStoreNameParam).(string)

	if a.secretStores[secretStoreName] == nil {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrSecretStoreNotFound, secretStoreName))
		respond(ctx, withError(fasthttp.StatusUnauthorized, msg))
		return nil, "", errors.New(msg.Message)
	}
	return a.secretStores[secretStoreName], secretStoreName, nil
}

func (a *api) onPostState(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(ctx)
	if err != nil {
		log.Debug(err)
		return
	}

	reqs := []state.SetRequest{}
	err = json.Unmarshal(ctx.PostBody(), &reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	if len(reqs) == 0 {
		respond(ctx, withEmpty())
		return
	}

	metadata := getMetadataFromRequest(ctx)

	for i, r := range reqs {
		// merge metadata from URL query parameters
		if reqs[i].Metadata == nil {
			reqs[i].Metadata = metadata
		} else {
			for k, v := range metadata {
				reqs[i].Metadata[k] = v
			}
		}

		reqs[i].Key, err = stateLoader.GetModifiedStateKey(r.Key, storeName, a.id)
		if err != nil {
			msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
			respond(ctx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(err)
			return
		}

		if encryption.EncryptedStateStore(storeName) {
			data := []byte(fmt.Sprintf("%v", r.Value))
			val, encErr := encryption.TryEncryptValue(storeName, data)
			if encErr != nil {
				statusCode, errMsg, resp := a.stateErrorResponse(encErr, "ERR_STATE_SAVE")
				resp.Message = fmt.Sprintf(messages.ErrStateSave, storeName, errMsg)

				respond(ctx, withError(statusCode, resp))
				log.Debug(resp.Message)
				return
			}

			reqs[i].Value = val
		}
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, store.BulkSet(ctx, reqs)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, storeName, diag.Set, err == nil, elapsed)

	if err != nil {
		storeName := a.getStateStoreName(ctx)

		statusCode, errMsg, resp := a.stateErrorResponse(err, "ERR_STATE_SAVE")
		resp.Message = fmt.Sprintf(messages.ErrStateSave, storeName, errMsg)

		respond(ctx, withError(statusCode, resp))
		log.Debug(resp.Message)
		return
	}

	respond(ctx, withEmpty())
}

// stateErrorResponse takes a state store error and returns a corresponding status code, error message and modified user error.
func (a *api) stateErrorResponse(err error, errorCode string) (int, string, ErrorResponse) {
	var message string
	var code int
	var etag bool
	etag, code, message = a.etagError(err)

	r := ErrorResponse{
		ErrorCode: errorCode,
	}
	if etag {
		return code, message, r
	}
	message = err.Error()

	return fasthttp.StatusInternalServerError, message, r
}

// etagError checks if the error from the state store is an etag error and returns a bool for indication,
// an status code and an error message.
func (a *api) etagError(err error) (bool, int, string) {
	e, ok := err.(*state.ETagError)
	if !ok {
		return false, -1, ""
	}
	switch e.Kind() {
	case state.ETagMismatch:
		return true, fasthttp.StatusConflict, e.Error()
	case state.ETagInvalid:
		return true, fasthttp.StatusBadRequest, e.Error()
	}

	return false, -1, ""
}

func (a *api) getStateStoreName(ctx *fasthttp.RequestCtx) string {
	return ctx.UserValue(storeNameParam).(string)
}

type invokeError struct {
	statusCode int
	msg        ErrorResponse
}

func (ie invokeError) Error() string {
	return fmt.Sprintf("invokeError (statusCode='%d') msg.errorCode='%s' msg.message='%s'", ie.statusCode, ie.msg.ErrorCode, ie.msg.Message)
}

type directMessagingPolicyRes struct {
	body        []byte
	statusCode  int
	contentType string
	headers     invokev1.DaprInternalMetadata
}

func (a *api) onDirectMessage(ctx *fasthttp.RequestCtx) {
	targetID := a.findTargetID(ctx)
	if targetID == "" {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNoAppID)
		respond(ctx, withError(fasthttp.StatusNotFound, msg))
		return
	}

	verb := strings.ToUpper(string(ctx.Method()))
	invokeMethodName := ctx.UserValue(methodParam).(string)

	if a.directMessaging == nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", messages.ErrDirectInvokeNotReady)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		return
	}

	policyDef := a.resiliency.EndpointPolicy(ctx, targetID, targetID+":"+invokeMethodName)

	// Construct internal invoke method request
	req := invokev1.NewInvokeMethodRequest(invokeMethodName).
		WithHTTPExtension(verb, ctx.QueryArgs().String()).
		WithRawDataBytes(ctx.Request.Body()).
		WithContentType(string(ctx.Request.Header.ContentType())).
		// Save headers to internal metadata
		WithFastHTTPHeaders(&ctx.Request.Header)
	if policyDef != nil {
		req.WithReplay(policyDef.HasRetries())
	}
	defer req.Close()

	policyRunner := resiliency.NewRunner[*directMessagingPolicyRes](ctx, policyDef)
	// Since we don't want to return the actual error, we have to extract several things in order to construct our response.
	dmpr, err := policyRunner(func(ctx context.Context) (*directMessagingPolicyRes, error) {
		dmpr := &directMessagingPolicyRes{}

		rResp, rErr := a.directMessaging.Invoke(ctx, targetID, req)
		if rResp != nil {
			defer rResp.Close()
		}
		if rErr != nil {
			// Allowlists policies that are applied on the callee side can return a Permission Denied error.
			// For everything else, treat it as a gRPC transport error
			invokeErr := invokeError{
				statusCode: fasthttp.StatusInternalServerError,
				msg:        NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, rErr)),
			}
			if status.Code(rErr) == codes.PermissionDenied {
				invokeErr.statusCode = invokev1.HTTPStatusFromCode(codes.PermissionDenied)
			}
			return dmpr, invokeErr
		}

		dmpr.headers = rResp.Headers()
		dmpr.contentType = rResp.ContentType()
		dmpr.body, rErr = rResp.RawDataFull()
		if rErr != nil {
			return dmpr, invokeError{
				statusCode: fasthttp.StatusInternalServerError,
				msg:        NewErrorResponse("ERR_DIRECT_INVOKE", fmt.Sprintf(messages.ErrDirectInvoke, targetID, rErr)),
			}
		}

		// Construct response
		dmpr.statusCode = int(rResp.Status().Code)
		if !rResp.IsHTTPResponse() {
			dmpr.statusCode = invokev1.HTTPStatusFromCode(codes.Code(dmpr.statusCode))
			if dmpr.statusCode != fasthttp.StatusOK {
				if dmpr.body, rErr = invokev1.ProtobufToJSON(rResp.Status()); rErr != nil {
					return dmpr, invokeError{
						statusCode: fasthttp.StatusInternalServerError,
						msg:        NewErrorResponse("ERR_MALFORMED_RESPONSE", rErr.Error()),
					}
				}
			}
		} else if dmpr.statusCode != fasthttp.StatusOK {
			return dmpr, fmt.Errorf("Received non-successful status code: %d", dmpr.statusCode) //nolint:stylecheck
		}
		return dmpr, nil
	})

	// Special case for timeouts/circuit breakers since they won't go through the rest of the logic.
	if errors.Is(err, context.DeadlineExceeded) || breaker.IsErrorPermanent(err) {
		respond(ctx, withError(fasthttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", err.Error())))
		return
	}

	if dmpr != nil && len(dmpr.headers) > 0 {
		invokev1.InternalMetadataToHTTPHeader(ctx, dmpr.headers, ctx.Response.Header.Set)
	}

	invokeErr := invokeError{}
	if errors.As(err, &invokeErr) {
		respond(ctx, withError(invokeErr.statusCode, invokeErr.msg))
		return
	}

	if dmpr == nil {
		respond(ctx, withError(fasthttp.StatusInternalServerError, NewErrorResponse("ERR_DIRECT_INVOKE", "response object is nil")))
		return
	}

	ctx.Response.Header.SetContentType(dmpr.contentType)
	respond(ctx, with(dmpr.statusCode, dmpr.body))
}

// findTargetID tries to find ID of the target service from the following three places:
// 1. {id} in the URL's path.
// 2. Basic authentication, http://dapr-app-id:<service-id>@localhost:3500/path.
// 3. HTTP header: 'dapr-app-id'.
func (a *api) findTargetID(ctx *fasthttp.RequestCtx) string {
	if id := ctx.UserValue(idParam); id == nil {
		if appID := ctx.Request.Header.Peek(daprAppID); appID == nil {
			if auth := ctx.Request.Header.Peek(fasthttp.HeaderAuthorization); auth != nil &&
				strings.HasPrefix(string(auth), "Basic ") {
				if s, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(auth), "Basic ")); err == nil {
					pair := strings.Split(string(s), ":")
					if len(pair) == 2 && pair[0] == daprAppID {
						return pair[1]
					}
				}
			}
		} else {
			return string(appID)
		}
	} else {
		return id.(string)
	}

	return ""
}

func (a *api) onCreateActorReminder(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	var req actors.CreateReminderRequest
	err := json.Unmarshal(ctx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateReminder(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", fmt.Sprintf(messages.ErrActorReminderCreate, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onRenameActorReminder(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	var req actors.RenameReminderRequest
	err := json.Unmarshal(ctx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.OldName = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.RenameReminder(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_RENAME", fmt.Sprintf(messages.ErrActorReminderRename, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onCreateActorTimer(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	var req actors.CreateTimerRequest
	err := json.Unmarshal(ctx.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateTimer(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", fmt.Sprintf(messages.ErrActorTimerCreate, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onDeleteActorReminder(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	req := actors.DeleteReminderRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}

	err := a.actor.DeleteReminder(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_DELETE", fmt.Sprintf(messages.ErrActorReminderDelete, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onActorStateTransaction(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	body := ctx.PostBody()

	var ops []actors.TransactionalOperation
	err := json.Unmarshal(body, &ops)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	hosted := a.actor.IsActorHosted(ctx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", messages.ErrActorInstanceMissing)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: ops,
	}

	err = a.actor.TransactionalStateOperation(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_TRANSACTION_SAVE", fmt.Sprintf(messages.ErrActorStateTransactionSave, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onGetActorReminder(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	resp, err := a.actor.GetReminder(ctx, &actors.GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      name,
	})
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", fmt.Sprintf(messages.ErrActorReminderGet, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	b, err := json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", fmt.Sprintf(messages.ErrActorReminderGet, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	respond(ctx, withJSON(fasthttp.StatusOK, b))
}

func (a *api) onDeleteActorTimer(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	name := ctx.UserValue(nameParam).(string)

	req := actors.DeleteTimerRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}
	err := a.actor.DeleteTimer(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_DELETE", fmt.Sprintf(messages.ErrActorTimerDelete, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onDirectActorMessage(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	verb := strings.ToUpper(string(ctx.Method()))
	method := ctx.UserValue(methodParam).(string)

	metadata := make(map[string][]string, ctx.Request.Header.Len())
	ctx.Request.Header.VisitAll(func(key []byte, value []byte) {
		metadata[string(key)] = []string{string(value)}
	})

	policyDef := a.resiliency.ActorPreLockPolicy(ctx, actorType, actorID)

	req := invokev1.NewInvokeMethodRequest(method).
		WithActor(actorType, actorID).
		WithHTTPExtension(verb, ctx.QueryArgs().String()).
		WithRawDataBytes(ctx.PostBody()).
		WithContentType(string(ctx.Request.Header.ContentType())).
		// Save headers to metadata
		WithMetadata(metadata)
	if policyDef != nil {
		req.WithReplay(policyDef.HasRetries())
	}
	defer req.Close()

	// Unlike other actor calls, resiliency is handled here for invocation.
	// This is due to actor invocation involving a lookup for the host.
	// Having the retry here allows us to capture that and be resilient to host failure.
	// Additionally, we don't perform timeouts at this level. This is because an actor
	// should technically wait forever on the locking mechanism. If we timeout while
	// waiting for the lock, we can also create a queue of calls that will try and continue
	// after the timeout.
	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.actor.Call(ctx, req)
	})
	if err != nil && !errors.Is(err, actors.ErrDaprResponseHeader) {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	if resp == nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, "failed to cast response"))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	defer resp.Close()

	invokev1.InternalMetadataToHTTPHeader(ctx, resp.Headers(), ctx.Response.Header.Set)
	body, err := resp.RawDataFull()
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", fmt.Sprintf(messages.ErrActorInvoke, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	ctx.Response.Header.SetContentType(resp.ContentType())

	// Construct response.
	statusCode := int(resp.Status().Code)
	if !resp.IsHTTPResponse() {
		statusCode = invokev1.HTTPStatusFromCode(codes.Code(statusCode))
	}
	respond(ctx, with(statusCode, body))
}

func (a *api) onGetActorState(ctx *fasthttp.RequestCtx) {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", messages.ErrActorRuntimeNotFound)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	actorType := ctx.UserValue(actorTypeParam).(string)
	actorID := ctx.UserValue(actorIDParam).(string)
	key := ctx.UserValue(stateKeyParam).(string)

	hosted := a.actor.IsActorHosted(ctx, &actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", messages.ErrActorInstanceMissing)
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	}

	resp, err := a.actor.GetState(ctx, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_GET", fmt.Sprintf(messages.ErrActorStateGet, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		if resp == nil || resp.Data == nil {
			respond(ctx, withEmpty())
			return
		}
		respond(ctx, withJSON(fasthttp.StatusOK, resp.Data))
	}
}

func (a *api) onGetMetadata(ctx *fasthttp.RequestCtx) {
	temp := make(map[string]string)

	// Copy synchronously so it can be serialized to JSON.
	a.extendedMetadata.Range(func(key, value interface{}) bool {
		temp[key.(string)] = value.(string)

		return true
	})
	temp[daprRuntimeVersionKey] = a.daprRunTimeVersion
	activeActorsCount := []actors.ActiveActorsCount{}
	if a.actor != nil {
		activeActorsCount = a.actor.GetActiveActorsCount(ctx)
	}
	componentsCapabilties := a.getComponentsCapabilitesFn(ctx)
	components := a.getComponentsFn(ctx)
	registeredComponents := make([]registeredComponent, 0, len(components))
	for _, comp := range components {
		registeredComp := registeredComponent{
			Name:         comp.Name,
			Version:      comp.Spec.Version,
			Type:         comp.Spec.Type,
			Capabilities: getOrDefaultCapabilites(componentsCapabilties, comp.Name),
		}
		registeredComponents = append(registeredComponents, registeredComp)
	}

	subscriptions, err := a.getSubscriptionsFn(ctx)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_GET_SUBSCRIPTIONS", fmt.Sprintf(messages.ErrPubsubGetSubscriptions, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	ps := []pubsubSubscription{}
	for _, s := range subscriptions {
		ps = append(ps, pubsubSubscription{
			PubsubName:      s.PubsubName,
			Topic:           s.Topic,
			Metadata:        s.Metadata,
			DeadLetterTopic: s.DeadLetterTopic,
			Rules:           convertPubsubSubscriptionRules(s.Rules),
		})
	}

	mtd := metadata{
		ID:                   a.id,
		ActiveActorsCount:    activeActorsCount,
		Extended:             temp,
		RegisteredComponents: registeredComponents,
		Subscriptions:        ps,
	}

	mtdBytes, err := json.Marshal(mtd)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", fmt.Sprintf(messages.ErrMetadataGet, err))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withJSON(fasthttp.StatusOK, mtdBytes))
	}
}

func getOrDefaultCapabilites(dict map[string][]string, key string) []string {
	if val, ok := dict[key]; ok {
		return val
	}
	return make([]string, 0)
}

func convertPubsubSubscriptionRules(rules []*runtimePubsub.Rule) []*pubsubSubscriptionRule {
	out := make([]*pubsubSubscriptionRule, 0)
	for _, r := range rules {
		out = append(out, &pubsubSubscriptionRule{
			Match: fmt.Sprintf("%s", r.Match),
			Path:  r.Path,
		})
	}
	return out
}

func (a *api) onPutMetadata(ctx *fasthttp.RequestCtx) {
	key := fmt.Sprintf("%v", ctx.UserValue("key"))
	body := ctx.PostBody()
	a.extendedMetadata.Store(key, string(body))
	respond(ctx, withEmpty())
}

func (a *api) onShutdown(ctx *fasthttp.RequestCtx) {
	if !ctx.IsPost() {
		log.Warn("Please use POST method when invoking shutdown API")
	}

	respond(ctx, withEmpty())
	go func() {
		a.shutdown()
	}()
}

func (a *api) onPublish(ctx *fasthttp.RequestCtx) {
	thepubsub, pubsubName, topic, sc, errRes := a.validateAndGetPubsubAndTopic(ctx)
	if errRes != nil {
		respond(ctx, withError(sc, *errRes))

		return
	}

	body := ctx.PostBody()
	contentType := string(ctx.Request.Header.Peek("Content-Type"))
	metadata := getMetadataFromRequest(ctx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		msg := NewErrorResponse("ERR_PUBSUB_REQUEST_METADATA",
			fmt.Sprintf(messages.ErrMetadataGet, metaErr.Error()))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}

	// Extract trace context from context.
	span := diagUtils.SpanFromContext(ctx)
	// Populate W3C traceparent to cloudevent envelope
	corID := diag.SpanContextToW3CString(span.SpanContext())
	// Populate W3C tracestate to cloudevent envelope
	traceState := diag.TraceStateToW3CString(span.SpanContext())

	data := body

	if !rawPayload {
		envelope, err := runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
			ID:              a.id,
			Topic:           topic,
			DataContentType: contentType,
			Data:            body,
			TraceID:         corID,
			TraceState:      traceState,
			Pubsub:          pubsubName,
		})
		if err != nil {
			msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubCloudEventCreation, err.Error()))
			respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}

		features := thepubsub.Features()

		pubsub.ApplyMetadata(envelope, features, metadata)

		data, err = json.Marshal(envelope)
		if err != nil {
			msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error()))
			respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
			log.Debug(msg)
			return
		}
	}

	req := pubsub.PublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Data:       data,
		Metadata:   metadata,
	}

	start := time.Now()
	err := a.pubsubAdapter.Publish(ctx, &req)
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.PubsubEgressEvent(ctx, pubsubName, topic, err == nil, elapsed)

	if err != nil {
		status := fasthttp.StatusInternalServerError
		msg := NewErrorResponse("ERR_PUBSUB_PUBLISH_MESSAGE",
			fmt.Sprintf(messages.ErrPubsubPublishMessage, topic, pubsubName, err.Error()))

		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_FORBIDDEN", err.Error())
			status = fasthttp.StatusForbidden
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			msg = NewErrorResponse("ERR_PUBSUB_NOT_FOUND", err.Error())
			status = fasthttp.StatusBadRequest
		}

		respond(ctx, withError(status, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

type bulkPublishMessageEntry struct {
	EntryId     string            `json:"entryId,omitempty"` //nolint:stylecheck
	Event       interface{}       `json:"event"`
	ContentType string            `json:"contentType"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (a *api) onBulkPublish(ctx *fasthttp.RequestCtx) {
	thepubsub, pubsubName, topic, sc, errRes := a.validateAndGetPubsubAndTopic(ctx)
	if errRes != nil {
		respond(ctx, withError(sc, *errRes))

		return
	}

	body := ctx.PostBody()
	metadata := getMetadataFromRequest(ctx)
	rawPayload, metaErr := contribMetadata.IsRawPayload(metadata)
	if metaErr != nil {
		msg := NewErrorResponse("ERR_PUBSUB_REQUEST_METADATA",
			fmt.Sprintf(messages.ErrMetadataGet, metaErr.Error()))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}

	// Extract trace context from context.
	span := diagUtils.SpanFromContext(ctx)
	// Populate W3C tracestate to cloudevent envelope
	traceState := diag.TraceStateToW3CString(span.SpanContext())

	incomingEntries := make([]bulkPublishMessageEntry, 0)
	err := json.Unmarshal(body, &incomingEntries)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
			fmt.Sprintf(messages.ErrPubsubUnmarshal, topic, pubsubName, err.Error()))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)

		return
	}
	entries := make([]pubsub.BulkMessageEntry, len(incomingEntries))

	entryIdSet := map[string]struct{}{} //nolint:stylecheck

	for i, entry := range incomingEntries {
		var dBytes []byte

		dBytes, cErr := ConvertEventToBytes(entry.Event, entry.ContentType)
		if cErr != nil {
			msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubMarshal, topic, pubsubName, cErr.Error()))
			respond(ctx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}
		entries[i] = pubsub.BulkMessageEntry{
			Event:       dBytes,
			ContentType: entry.ContentType,
		}
		if entry.Metadata != nil {
			// Populate entry metadata with request level metadata. Entry level metadata keys
			// override request level metadata.
			entries[i].Metadata = utils.PopulateMetadataForBulkPublishEntry(metadata, entry.Metadata)
		}
		if _, ok := entryIdSet[entry.EntryId]; ok || entry.EntryId == "" {
			msg := NewErrorResponse("ERR_PUBSUB_EVENTS_SER",
				fmt.Sprintf(messages.ErrPubsubMarshal, topic, pubsubName, "error: entryId is duplicated or not present for entry"))
			respond(ctx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)

			return
		}
		entryIdSet[entry.EntryId] = struct{}{}
		entries[i].EntryId = entry.EntryId
	}

	spanMap := map[int]trace.Span{}
	// closeChildSpans method is called on every respond() call in all return paths in the following block of code.
	closeChildSpans := func(ctx *fasthttp.RequestCtx) {
		for _, span := range spanMap {
			diag.UpdateSpanStatusFromHTTPStatus(span, ctx.Response.StatusCode())
			span.End()
		}
	}
	features := thepubsub.Features()
	if !rawPayload {
		for i := range entries {
			// For multiple events in a single bulk call traceParent is different for each event.
			childSpan := diag.StartProducerSpanChildFromParent(ctx, span)
			// Populate W3C traceparent to cloudevent envelope
			corID := diag.SpanContextToW3CString(childSpan.SpanContext())
			spanMap[i] = childSpan

			var envelope map[string]interface{}

			envelope, err = runtimePubsub.NewCloudEvent(&runtimePubsub.CloudEvent{
				ID:              a.id,
				Topic:           topic,
				DataContentType: entries[i].ContentType,
				Data:            entries[i].Event,
				TraceID:         corID,
				TraceState:      traceState,
				Pubsub:          pubsubName,
			})
			if err != nil {
				msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
					fmt.Sprintf(messages.ErrPubsubCloudEventCreation, err.Error()))
				respond(ctx, withError(fasthttp.StatusInternalServerError, msg), closeChildSpans)
				log.Debug(msg)

				return
			}

			pubsub.ApplyMetadata(envelope, features, entries[i].Metadata)

			entries[i].Event, err = json.Marshal(envelope)
			if err != nil {
				msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER",
					fmt.Sprintf(messages.ErrPubsubCloudEventsSer, topic, pubsubName, err.Error()))
				respond(ctx, withError(fasthttp.StatusInternalServerError, msg), closeChildSpans)
				log.Debug(msg)

				return
			}
		}
	}

	req := pubsub.BulkPublishRequest{
		PubsubName: pubsubName,
		Topic:      topic,
		Entries:    entries,
		Metadata:   metadata,
	}

	start := time.Now()
	res, err := a.pubsubAdapter.BulkPublish(ctx, &req)
	elapsed := diag.ElapsedSince(start)

	// BulkPublishResponse contains all failed entries from the request.
	// If there are no errors, then an empty response is returned.
	bulkRes := BulkPublishResponse{}
	eventsPublished := int64(len(req.Entries))
	if len(res.FailedEntries) != 0 {
		eventsPublished -= int64(len(res.FailedEntries))
	}

	diag.DefaultComponentMonitoring.BulkPubsubEgressEvent(ctx, pubsubName, topic, err == nil, eventsPublished, elapsed)

	if err != nil {
		bulkRes.FailedEntries = make([]BulkPublishResponseFailedEntry, 0, len(res.FailedEntries))
		for _, r := range res.FailedEntries {
			resEntry := BulkPublishResponseFailedEntry{EntryId: r.EntryId}
			if r.Error != nil {
				resEntry.Error = r.Error.Error()
			}
			bulkRes.FailedEntries = append(bulkRes.FailedEntries, resEntry)
		}
		status := fasthttp.StatusInternalServerError
		bulkRes.ErrorCode = "ERR_PUBSUB_PUBLISH_MESSAGE"

		if errors.As(err, &runtimePubsub.NotAllowedError{}) {
			msg := NewErrorResponse("ERR_PUBSUB_FORBIDDEN", err.Error())
			status = fasthttp.StatusForbidden
			respond(ctx, withError(status, msg), closeChildSpans)
			log.Debug(msg)

			return
		}

		if errors.As(err, &runtimePubsub.NotFoundError{}) {
			msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", err.Error())
			status = fasthttp.StatusBadRequest
			respond(ctx, withError(status, msg), closeChildSpans)
			log.Debug(msg)

			return
		}

		// Return the error along with the list of failed entries.
		resData, _ := json.Marshal(bulkRes)
		respond(ctx, withJSON(status, resData), closeChildSpans)
		return
	}

	// If there are no errors, then an empty response is returned.
	respond(ctx, withEmpty(), closeChildSpans)
}

// validateAndGetPubsubAndTopic takes input as request context and returns the pubsub interface, pubsub name, topic name,
// or error status code and an ErrorResponse object.
func (a *api) validateAndGetPubsubAndTopic(ctx *fasthttp.RequestCtx) (pubsub.PubSub, string, string, int, *ErrorResponse) {
	if a.pubsubAdapter == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_CONFIGURED", messages.ErrPubsubNotConfigured)

		return nil, "", "", fasthttp.StatusBadRequest, &msg
	}

	pubsubName := ctx.UserValue(pubsubnameparam).(string)
	if pubsubName == "" {
		msg := NewErrorResponse("ERR_PUBSUB_EMPTY", messages.ErrPubsubEmpty)

		return nil, "", "", fasthttp.StatusNotFound, &msg
	}

	thepubsub := a.pubsubAdapter.GetPubSub(pubsubName)
	if thepubsub == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", fmt.Sprintf(messages.ErrPubsubNotFound, pubsubName))

		return nil, "", "", fasthttp.StatusNotFound, &msg
	}

	topic := ctx.UserValue(topicParam).(string)
	if topic == "" {
		msg := NewErrorResponse("ERR_TOPIC_EMPTY", fmt.Sprintf(messages.ErrTopicEmpty, pubsubName))

		return nil, "", "", fasthttp.StatusNotFound, &msg
	}
	return thepubsub, pubsubName, topic, fasthttp.StatusOK, nil
}

// GetStatusCodeFromMetadata extracts the http status code from the metadata if it exists.
func GetStatusCodeFromMetadata(metadata map[string]string) int {
	code := metadata[http.HTTPStatusCode]
	if code != "" {
		statusCode, err := strconv.Atoi(code)
		if err == nil {
			return statusCode
		}
	}

	return fasthttp.StatusOK
}

func (a *api) onGetHealthz(ctx *fasthttp.RequestCtx) {
	if !a.readyStatus {
		msg := NewErrorResponse("ERR_HEALTH_NOT_READY", messages.ErrHealthNotReady)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onGetOutboundHealthz(ctx *fasthttp.RequestCtx) {
	if !a.outboundReadyStatus {
		msg := NewErrorResponse("ERR_HEALTH_NOT_READY", messages.ErrHealthNotReady)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func getMetadataFromRequest(ctx *fasthttp.RequestCtx) map[string]string {
	metadata := map[string]string{}
	prefixBytes := []byte(metadataPrefix)
	ctx.QueryArgs().VisitAll(func(key []byte, value []byte) {
		if bytes.HasPrefix(key, prefixBytes) {
			k := string(key[len(prefixBytes):])
			metadata[k] = string(value)
		}
	})

	return metadata
}

func (a *api) onPostStateTransaction(ctx *fasthttp.RequestCtx) {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_CONFIGURED", messages.ErrStateStoresNotConfigured)
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	storeName := ctx.UserValue(storeNameParam).(string)
	_, ok := a.stateStores[storeName]
	if !ok {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf(messages.ErrStateStoreNotFound, storeName))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	transactionalStore, ok := a.transactionalStateStores[storeName]
	if !ok {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_SUPPORTED", fmt.Sprintf(messages.ErrStateStoreNotSupported, storeName))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}

	body := ctx.PostBody()
	var req state.TransactionalStateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	if len(req.Operations) == 0 {
		respond(ctx, withEmpty())
		return
	}

	// merge metadata from URL query parameters
	metadata := getMetadataFromRequest(ctx)
	if req.Metadata == nil {
		req.Metadata = metadata
	} else {
		for k, v := range metadata {
			req.Metadata[k] = v
		}
	}

	operations := make([]state.TransactionalStateOperation, len(req.Operations))
	for i, o := range req.Operations {
		switch o.Operation {
		case state.Upsert:
			var upsertReq state.SetRequest
			err := mapstructure.Decode(o.Request, &upsertReq)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST",
					fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
				respond(ctx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			upsertReq.Key, err = stateLoader.GetModifiedStateKey(upsertReq.Key, storeName, a.id)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
				respond(ctx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(err)
				return
			}
			operations[i] = state.TransactionalStateOperation{
				Request:   upsertReq,
				Operation: state.Upsert,
			}
		case state.Delete:
			var delReq state.DeleteRequest
			err := mapstructure.Decode(o.Request, &delReq)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST",
					fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
				respond(ctx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			delReq.Key, err = stateLoader.GetModifiedStateKey(delReq.Key, storeName, a.id)
			if err != nil {
				msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
				respond(ctx, withError(fasthttp.StatusBadRequest, msg))
				log.Debug(msg)
				return
			}
			operations[i] = state.TransactionalStateOperation{
				Request:   delReq,
				Operation: state.Delete,
			}
		default:
			msg := NewErrorResponse(
				"ERR_NOT_SUPPORTED_STATE_OPERATION",
				fmt.Sprintf(messages.ErrNotSupportedStateOperation, o.Operation))
			respond(ctx, withError(fasthttp.StatusBadRequest, msg))
			log.Debug(msg)
			return
		}
	}

	if encryption.EncryptedStateStore(storeName) {
		for i, op := range operations {
			if op.Operation == state.Upsert {
				req := op.Request.(*state.SetRequest)
				data := []byte(fmt.Sprintf("%v", req.Value))
				val, err := encryption.TryEncryptValue(storeName, data)
				if err != nil {
					msg := NewErrorResponse(
						"ERR_SAVE_STATE",
						fmt.Sprintf(messages.ErrStateSave, storeName, err.Error()))
					respond(ctx, withError(fasthttp.StatusBadRequest, msg))
					log.Debug(msg)
					return
				}

				req.Value = val
				operations[i].Request = req
			}
		}
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Statestore),
	)
	storeReq := &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   req.Metadata,
	}
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, transactionalStore.Multi(ctx, storeReq)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, storeName, diag.StateTransaction, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_TRANSACTION", fmt.Sprintf(messages.ErrStateTransaction, err.Error()))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
	} else {
		respond(ctx, withEmpty())
	}
}

func (a *api) onQueryState(ctx *fasthttp.RequestCtx) {
	store, storeName, err := a.getStateStoreWithRequestValidation(ctx)
	if err != nil {
		// error has been already logged
		return
	}

	querier, ok := store.(state.Querier)
	if !ok {
		msg := NewErrorResponse("ERR_METHOD_NOT_FOUND", fmt.Sprintf(messages.ErrNotFound, "Query"))
		respond(ctx, withError(fasthttp.StatusNotFound, msg))
		log.Debug(msg)
		return
	}

	if encryption.EncryptedStateStore(storeName) {
		msg := NewErrorResponse("ERR_STATE_QUERY", fmt.Sprintf(messages.ErrStateQuery, storeName, "cannot query encrypted store"))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}

	var req state.QueryRequest
	if err = json.Unmarshal(ctx.PostBody(), &req.Query); err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", fmt.Sprintf(messages.ErrMalformedRequest, err.Error()))
		respond(ctx, withError(fasthttp.StatusBadRequest, msg))
		log.Debug(msg)
		return
	}
	req.Metadata = getMetadataFromRequest(ctx)

	start := time.Now()
	policyRunner := resiliency.NewRunner[*state.QueryResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(ctx, storeName, resiliency.Statestore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*state.QueryResponse, error) {
		return querier.Query(ctx, &req)
	})
	elapsed := diag.ElapsedSince(start)

	diag.DefaultComponentMonitoring.StateInvoked(ctx, storeName, diag.StateQuery, err == nil, elapsed)

	if err != nil {
		msg := NewErrorResponse("ERR_STATE_QUERY", fmt.Sprintf(messages.ErrStateQuery, storeName, err.Error()))
		respond(ctx, withError(fasthttp.StatusInternalServerError, msg))
		log.Debug(msg)
		return
	}
	if resp == nil || len(resp.Results) == 0 {
		respond(ctx, withEmpty())
		return
	}

	qresp := QueryResponse{
		Results:  make([]QueryItem, len(resp.Results)),
		Token:    resp.Token,
		Metadata: resp.Metadata,
	}
	for i := range resp.Results {
		qresp.Results[i].Key = stateLoader.GetOriginalStateKey(resp.Results[i].Key)
		qresp.Results[i].ETag = resp.Results[i].ETag
		qresp.Results[i].Error = resp.Results[i].Error
		qresp.Results[i].Data = json.RawMessage(resp.Results[i].Data)
	}

	b, _ := json.Marshal(qresp)
	respond(ctx, withJSON(fasthttp.StatusOK, b))
}

func (a *api) isSecretAllowed(storeName, key string) bool {
	if config, ok := a.secretsConfiguration[storeName]; ok {
		return config.IsSecretAllowed(key)
	}
	// By default, if a configuration is not defined for a secret store, return true.
	return true
}

func (a *api) SetAppChannel(appChannel channel.AppChannel) {
	a.appChannel = appChannel
}

func (a *api) SetDirectMessaging(directMessaging messaging.DirectMessaging) {
	a.directMessaging = directMessaging
}

func (a *api) SetActorRuntime(actor actors.Actors) {
	a.actor = actor
}
