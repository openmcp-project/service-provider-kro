/*
Copyright 2025.

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

package v1alpha1_test

import (
	"testing"
	"time"

	ctrlerrors "github.com/openmcp-project/controller-utils/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/openmcp-project/service-provider-kro/api/v1alpha1"
)

func providerConfig(versions ...v1alpha1.KroVersion) *v1alpha1.ProviderConfig {
	return &v1alpha1.ProviderConfig{
		Spec: v1alpha1.ProviderConfigSpec{Versions: versions},
	}
}

func TestResolveVersion(t *testing.T) {
	// The tenant facing version is matched verbatim and need not equal the chart tag:
	// "v0.9.3" here deliberately maps onto the unprefixed chart version "0.9.3".
	prefixed := v1alpha1.KroVersion{
		Version:         "v0.9.3",
		ChartVersion:    "0.9.3",
		ChartURL:        ptr.To("oci://registry.k8s.io/kro/charts/kro"),
		ChartPullSecret: "regcred",
	}
	plain := v1alpha1.KroVersion{Version: "0.9.1", ChartVersion: "0.9.1"}

	tests := []struct {
		name      string
		config    *v1alpha1.ProviderConfig
		requested string
		want      v1alpha1.KroVersion
		wantErr   string
	}{
		{
			name:      "resolves a v-prefixed version onto its chart version",
			config:    providerConfig(prefixed, plain),
			requested: "v0.9.3",
			want:      prefixed,
		},
		{
			name:      "resolves an unprefixed version",
			config:    providerConfig(prefixed, plain),
			requested: "0.9.1",
			want:      plain,
		},
		{
			name:      "match is verbatim: a v prefix is not inferred",
			config:    providerConfig(prefixed),
			requested: "0.9.3",
			wantErr:   `"0.9.3", available versions are: v0.9.3`,
		},
		{
			name:      "unknown version lists what is on offer",
			config:    providerConfig(prefixed, plain),
			requested: "9.9.9",
			wantErr:   `"9.9.9", available versions are: v0.9.3, 0.9.1`,
		},
		{
			name:      "empty version list",
			config:    providerConfig(),
			requested: "0.9.3",
			wantErr:   "the provider config offers no versions",
		},
		{
			name:      "nil provider config",
			config:    nil,
			requested: "0.9.3",
			wantErr:   "no provider config is configured",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.config.ResolveVersion(tc.requested)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorIs(t, err, v1alpha1.ErrVersionNotAvailable)
				assert.ErrorIs(t, err, ctrlerrors.ErrInvalidUserInput)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestKroVersionGetChartURL(t *testing.T) {
	tests := []struct {
		name string
		url  *string
		want string
	}{
		{name: "unset falls back to the default", url: nil, want: v1alpha1.DefaultChartURL},
		{name: "empty falls back to the default", url: ptr.To(""), want: v1alpha1.DefaultChartURL},
		{name: "scheme is preserved", url: ptr.To("oci://registry.example.com/kro"), want: "oci://registry.example.com/kro"},
		{name: "missing scheme is added", url: ptr.To("registry.example.com/kro"), want: "oci://registry.example.com/kro"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, v1alpha1.KroVersion{ChartURL: tc.url}.GetChartURL())
		})
	}
}

func TestPollInterval(t *testing.T) {
	// Nil-safe on both the receiver and the field: the CRD default does not apply to
	// objects built in Go, and a missing interval must not panic the reconcile loop.
	assert.Equal(t, v1alpha1.DefaultPollInterval, (*v1alpha1.ProviderConfig)(nil).PollInterval())
	assert.Equal(t, v1alpha1.DefaultPollInterval, providerConfig().PollInterval())

	pc := providerConfig()
	pc.Spec.PollInterval = &metav1.Duration{Duration: 5 * time.Minute}
	assert.Equal(t, 5*time.Minute, pc.PollInterval())
}
