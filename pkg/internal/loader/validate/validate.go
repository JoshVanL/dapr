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

// Package validate provides CRD-based validation for Dapr resources in
// standalone mode. It parses the embedded CRD YAML, builds a structural
// schema, and validates resources against both OpenAPI schema constraints
// (enum, minLength, minItems, required, etc.) and CEL XValidation rules.
// This gives standalone mode the same validation the Kubernetes API server
// provides via CRD admission.
package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	apiextinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextconv "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	apivalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.loader.validate")

// Validator validates resources against the OpenAPI schema and CEL rules
// embedded in a CRD YAML. It is safe for concurrent use.
type Validator struct {
	celValidator    *cel.Validator
	schemaValidator apivalidation.SchemaValidator
	initOnce        sync.Once
	initErr         error
	crdYAML         []byte
	name            string
}

// NewValidator creates a Validator that will lazily parse the given CRD YAML
// on first use. The name is used for log messages.
func NewValidator(name string, crdYAML []byte) *Validator {
	return &Validator{
		crdYAML: crdYAML,
		name:    name,
	}
}

func (v *Validator) init() {
	scheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		v.initErr = fmt.Errorf("failed to add apiextensions to scheme: %w", err)
		return
	}

	codecs := serializer.NewCodecFactory(scheme)
	obj, _, err := codecs.UniversalDeserializer().Decode(v.crdYAML, nil, nil)
	if err != nil {
		v.initErr = fmt.Errorf("failed to decode CRD YAML: %w", err)
		return
	}

	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		v.initErr = fmt.Errorf("decoded object is %T, not CustomResourceDefinition", obj)
		return
	}

	if len(crd.Spec.Versions) == 0 || crd.Spec.Versions[0].Schema == nil ||
		crd.Spec.Versions[0].Schema.OpenAPIV3Schema == nil {
		v.initErr = fmt.Errorf("CRD has no OpenAPI schema")
		return
	}

	v1Schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema

	// Convert v1 JSONSchemaProps to internal for both validators.
	var internalSchema apiextinternal.JSONSchemaProps
	if err := apiextconv.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		v1Schema, &internalSchema, nil,
	); err != nil {
		v.initErr = fmt.Errorf("failed to convert schema to internal: %w", err)
		return
	}

	// Build the OpenAPI schema validator (validates enum, minLength, minItems,
	// required, type constraints, etc.).
	schemaValidator, _, err := apivalidation.NewSchemaValidator(&internalSchema)
	if err != nil {
		v.initErr = fmt.Errorf("failed to create schema validator: %w", err)
		return
	}
	v.schemaValidator = schemaValidator

	// Build structural schema for CEL validation.
	structural, err := structuralschema.NewStructural(&internalSchema)
	if err != nil {
		v.initErr = fmt.Errorf("failed to create structural schema: %w", err)
		return
	}

	// Compile CEL validators from XValidation rules (if any).
	v.celValidator = cel.NewValidator(structural, true, celconfig.PerCallLimit)
}

// Validate checks the given resource against both the CRD's OpenAPI schema
// constraints (enum, minLength, minItems, required, etc.) and any CEL
// XValidation rules. The resource must be JSON-serializable. Returns nil if valid.
func (v *Validator) Validate(resource any) error {
	v.initOnce.Do(v.init)
	if v.initErr != nil {
		log.Warnf("%s validator unavailable, skipping validation: %s", v.name, v.initErr)
		return nil
	}

	raw, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", v.name, err)
	}

	var obj interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("failed to unmarshal %s: %w", v.name, err)
	}

	// Validate against OpenAPI schema (enum, minLength, minItems, etc.).
	errs := apivalidation.ValidateCustomResource(field.NewPath(""), obj, v.schemaValidator)

	// Validate against CEL XValidation rules (if any are defined in the CRD).
	celErrs, _ := v.celValidator.Validate(
		context.Background(),
		field.NewPath(""),
		nil,
		obj,
		nil,
		celconfig.RuntimeCELCostBudget,
	)
	errs = append(errs, celErrs...)

	if len(errs) > 0 {
		return errs.ToAggregate()
	}

	return nil
}
