/*
Copyright 2020 The Flux authors

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

package controllers

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/chartutil"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	v2 "github.com/fluxcd/helm-controller/api/v2beta1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta2"
)

func TestHelmReleaseReconciler_composeValues(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v2.AddToScheme(scheme)

	tests := []struct {
		name       string
		resources  []runtime.Object
		references []v2.ValuesReference
		values     string
		want       chartutil.Values
		wantErr    bool
	}{
		{
			name: "merges",
			resources: []runtime.Object{
				valuesConfigMap("values", map[string]string{
					"values.yaml": `flat: value
nested:
  configuration: value
`,
				}),
				valuesSecret("values", map[string][]byte{
					"values.yaml": []byte(`flat:
  nested: value
nested: value
`),
				}),
			},
			references: []v2.ValuesReference{
				{
					Kind: "ConfigMap",
					Name: "values",
				},
				{
					Kind: "Secret",
					Name: "values",
				},
			},
			values: `
other: values
`,
			want: chartutil.Values{
				"flat": map[string]interface{}{
					"nested": "value",
				},
				"nested": "value",
				"other":  "values",
			},
		},
		{
			name: "target path",
			resources: []runtime.Object{
				valuesSecret("values", map[string][]byte{"single": []byte("value")}),
			},
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					ValuesKey:  "single",
					TargetPath: "merge.at.specific.path",
				},
			},
			want: chartutil.Values{
				"merge": map[string]interface{}{
					"at": map[string]interface{}{
						"specific": map[string]interface{}{
							"path": "value",
						},
					},
				},
			},
		},
		{
			name: "target path with boolean value",
			resources: []runtime.Object{
				valuesSecret("values", map[string][]byte{"single": []byte("true")}),
			},
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					ValuesKey:  "single",
					TargetPath: "merge.at.specific.path",
				},
			},
			want: chartutil.Values{
				"merge": map[string]interface{}{
					"at": map[string]interface{}{
						"specific": map[string]interface{}{
							"path": true,
						},
					},
				},
			},
		},
		{
			name: "target path with set-string behavior",
			resources: []runtime.Object{
				valuesSecret("values", map[string][]byte{"single": []byte("\"true\"")}),
			},
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					ValuesKey:  "single",
					TargetPath: "merge.at.specific.path",
				},
			},
			want: chartutil.Values{
				"merge": map[string]interface{}{
					"at": map[string]interface{}{
						"specific": map[string]interface{}{
							"path": "true",
						},
					},
				},
			},
		},
		{
			name: "values reference to non existing secret",
			references: []v2.ValuesReference{
				{
					Kind: "Secret",
					Name: "missing",
				},
			},
			wantErr: true,
		},
		{
			name: "optional values reference to non existing secret",
			references: []v2.ValuesReference{
				{
					Kind:     "Secret",
					Name:     "missing",
					Optional: true,
				},
			},
			want:    chartutil.Values{},
			wantErr: false,
		},
		{
			name: "values reference to non existing config map",
			references: []v2.ValuesReference{
				{
					Kind: "ConfigMap",
					Name: "missing",
				},
			},
			wantErr: true,
		},
		{
			name: "optional values reference to non existing config map",
			references: []v2.ValuesReference{
				{
					Kind:     "ConfigMap",
					Name:     "missing",
					Optional: true,
				},
			},
			want:    chartutil.Values{},
			wantErr: false,
		},
		{
			name: "missing secret key",
			resources: []runtime.Object{
				valuesSecret("values", nil),
			},
			references: []v2.ValuesReference{
				{
					Kind:      "Secret",
					Name:      "values",
					ValuesKey: "nonexisting",
				},
			},
			wantErr: true,
		},
		{
			name: "missing config map key",
			resources: []runtime.Object{
				valuesConfigMap("values", nil),
			},
			references: []v2.ValuesReference{
				{
					Kind:      "ConfigMap",
					Name:      "values",
					ValuesKey: "nonexisting",
				},
			},
			wantErr: true,
		},
		{
			name: "unsupported values reference kind",
			references: []v2.ValuesReference{
				{
					Kind: "Unsupported",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid values",
			resources: []runtime.Object{
				valuesConfigMap("values", map[string]string{
					"values.yaml": `
invalid`,
				}),
			},
			references: []v2.ValuesReference{
				{
					Kind: "ConfigMap",
					Name: "values",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewFakeClientWithScheme(scheme, tt.resources...)
			r := &HelmReleaseReconciler{Client: c}
			var values *apiextensionsv1.JSON
			if tt.values != "" {
				v, _ := yaml.YAMLToJSON([]byte(tt.values))
				values = &apiextensionsv1.JSON{Raw: v}
			}
			hr := v2.HelmRelease{
				Spec: v2.HelmReleaseSpec{
					ValuesFrom: tt.references,
					Values:     values,
				},
			}
			got, err := r.composeValues(logr.NewContext(context.TODO(), logr.Discard()), hr)
			if (err != nil) != tt.wantErr {
				t.Errorf("composeValues() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("composeValues() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValuesReferenceValidation(t *testing.T) {
	tests := []struct {
		name       string
		references []v2.ValuesReference
		wantErr    bool
	}{
		{
			name: "valid ValuesKey",
			references: []v2.ValuesReference{
				{
					Kind:      "Secret",
					Name:      "values",
					ValuesKey: "any-key_na.me",
				},
			},
			wantErr: false,
		},
		{
			name: "valid ValuesKey: empty",
			references: []v2.ValuesReference{
				{
					Kind:      "Secret",
					Name:      "values",
					ValuesKey: "",
				},
			},
			wantErr: false,
		},
		{
			name: "valid ValuesKey: long",
			references: []v2.ValuesReference{
				{
					Kind:      "Secret",
					Name:      "values",
					ValuesKey: strings.Repeat("a", 253),
				},
			},
			wantErr: false,
		},
		{
			name: "invalid ValuesKey",
			references: []v2.ValuesReference{
				{
					Kind:      "Secret",
					Name:      "values",
					ValuesKey: "a($&^%b",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid ValuesKey: too long",
			references: []v2.ValuesReference{
				{
					Kind:      "Secret",
					Name:      "values",
					ValuesKey: strings.Repeat("a", 254),
				},
			},
			wantErr: true,
		},
		{
			name: "valid target path: empty",
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					TargetPath: "",
				},
			},
			wantErr: false,
		},
		{
			name: "valid target path",
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					TargetPath: "list_with.nested-values.and.index[0]",
				},
			},
			wantErr: false,
		},
		{
			name: "valid target path: long",
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					TargetPath: strings.Repeat("a", 250),
				},
			},
			wantErr: false,
		},
		{
			name: "invalid target path: too long",
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					TargetPath: strings.Repeat("a", 251),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid target path: opened index",
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					ValuesKey:  "single",
					TargetPath: "a[",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid target path: incorrect index syntax",
			references: []v2.ValuesReference{
				{
					Kind:       "Secret",
					Name:       "values",
					ValuesKey:  "single",
					TargetPath: "a]0[",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var values *apiextensionsv1.JSON
			v, _ := yaml.YAMLToJSON([]byte("values"))
			values = &apiextensionsv1.JSON{Raw: v}

			hr := v2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: v2.HelmReleaseSpec{
					Interval: metav1.Duration{Duration: 5 * time.Minute},
					Chart: v2.HelmChartTemplate{
						Spec: v2.HelmChartTemplateSpec{
							SourceRef: v2.CrossNamespaceObjectReference{
								Name: "something",
							},
						},
					},
					ValuesFrom: tt.references,
					Values:     values,
				},
			}

			err := k8sClient.Create(context.TODO(), &hr, client.DryRunAll)
			if (err != nil) != tt.wantErr {
				t.Errorf("composeValues() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func FuzzHelmReleaseReconciler_composeValues(f *testing.F) {
	scheme := testScheme()

	tests := []struct {
		targetPath   string
		valuesKey    string
		hrValues     string
		createObject bool
		secretData   []byte
		configData   string
	}{
		{
			targetPath: "flat",
			valuesKey:  "custom-values.yaml",
			secretData: []byte(`flat:
  nested: value
nested: value
`),
			configData: `flat: value
nested:
  configuration: value
`,
			hrValues: `
other: values
`,
			createObject: true,
		},
		{
			targetPath: "'flat'",
			valuesKey:  "custom-values.yaml",
			secretData: []byte(`flat:
  nested: value
nested: value
`),
			configData: `flat: value
nested:
  configuration: value
`,
			hrValues: `
other: values
`,
			createObject: true,
		},
		{
			targetPath: "flat[0]",
			secretData: []byte(``),
			configData: `flat: value`,
			hrValues: `
other: values
`,
			createObject: true,
		},
		{
			secretData: []byte(`flat:
  nested: value
nested: value
`),
			configData: `flat: value
nested:
  configuration: value
`,
			hrValues: `
other: values
`,
			createObject: true,
		},
		{
			targetPath: "some-value",
			hrValues: `
other: values
`,
			createObject: false,
		},
	}

	for _, tt := range tests {
		f.Add(tt.targetPath, tt.valuesKey, tt.hrValues, tt.createObject, tt.secretData, tt.configData)
	}

	f.Fuzz(func(t *testing.T,
		targetPath, valuesKey, hrValues string, createObject bool, secretData []byte, configData string) {

		// objectName represents a core Kubernetes name (Secret/ConfigMap) which is validated
		// upstream, and also validated by us in the OpenAPI-based validation set in
		// v2.ValuesReference. Therefore a static value here suffices, and instead we just
		// play with the objects presence/absence.
		objectName := "values"
		resources := []runtime.Object{}

		if createObject {
			resources = append(resources,
				valuesConfigMap(objectName, map[string]string{valuesKey: configData}),
				valuesSecret(objectName, map[string][]byte{valuesKey: secretData}),
			)
		}

		references := []v2.ValuesReference{
			{
				Kind:       "ConfigMap",
				Name:       objectName,
				ValuesKey:  valuesKey,
				TargetPath: targetPath,
			},
			{
				Kind:       "Secret",
				Name:       objectName,
				ValuesKey:  valuesKey,
				TargetPath: targetPath,
			},
		}

		c := fake.NewFakeClientWithScheme(scheme, resources...)
		r := &HelmReleaseReconciler{Client: c}
		var values *apiextensionsv1.JSON
		if hrValues != "" {
			v, _ := yaml.YAMLToJSON([]byte(hrValues))
			values = &apiextensionsv1.JSON{Raw: v}
		}

		hr := v2.HelmRelease{
			Spec: v2.HelmReleaseSpec{
				ValuesFrom: references,
				Values:     values,
			},
		}

		// OpenAPI-based validation on schema is not verified here.
		// Therefore some false positives may be arise, as the apiserver
		// would not allow such values to make their way into the control plane.
		//
		// Testenv could be used so the fuzzing covers the entire E2E.
		// The downsize being the resource and time cost per test would be a lot higher.
		//
		// Another approach could be to add validation to reject invalid inputs before
		// the r.composeValues call.
		_, _ = r.composeValues(logr.NewContext(context.TODO(), logr.Discard()), hr)
	})
}

func FuzzHelmReleaseReconciler_reconcile(f *testing.F) {
	scheme := testScheme()
	tests := []struct {
		valuesKey  string
		hrValues   string
		secretData []byte
		configData string
	}{
		{
			valuesKey: "custom-values.yaml",
			secretData: []byte(`flat:
  nested: value
nested: value
`),
			configData: `flat: value
nested:
  configuration: value
`,
			hrValues: `
other: values
`,
		},
	}

	for _, tt := range tests {
		f.Add(tt.valuesKey, tt.hrValues, tt.secretData, tt.configData)
	}

	f.Fuzz(func(t *testing.T,
		valuesKey, hrValues string, secretData []byte, configData string) {

		var values *apiextensionsv1.JSON
		if hrValues != "" {
			v, _ := yaml.YAMLToJSON([]byte(hrValues))
			values = &apiextensionsv1.JSON{Raw: v}
		}

		hr := v2.HelmRelease{
			Spec: v2.HelmReleaseSpec{
				Values: values,
			},
		}

		hc := sourcev1.HelmChart{}
		hc.ObjectMeta.Name = hr.GetHelmChartName()
		hc.ObjectMeta.Namespace = hr.Spec.Chart.GetNamespace(hr.Namespace)

		resources := []runtime.Object{
			valuesConfigMap("values", map[string]string{valuesKey: configData}),
			valuesSecret("values", map[string][]byte{valuesKey: secretData}),
			&hc,
		}

		c := fake.NewFakeClientWithScheme(scheme, resources...)
		r := &HelmReleaseReconciler{
			Client:        c,
			EventRecorder: &DummyRecorder{},
		}

		_, _, _ = r.reconcile(logr.NewContext(context.TODO(), logr.Discard()), hr)
	})
}

func valuesSecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       data,
	}
}

func valuesConfigMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       data,
	}
}

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v2.AddToScheme(scheme)
	_ = sourcev1.AddToScheme(scheme)
	return scheme
}

// DummyRecorder serves as a dummy for kuberecorder.EventRecorder.
type DummyRecorder struct{}

func (r *DummyRecorder) Event(object runtime.Object, eventtype, reason, message string) {
}

func (r *DummyRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (r *DummyRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string,
	eventtype, reason string, messageFmt string, args ...interface{}) {
}
