// Copyright  Splunk, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
)

func TestFuncMap(t *testing.T) {
	componentID := component.MustNewID("redis")
	fm := funcMap(componentID)
	functions := []string{
		"configProperty",
		"configPropertyEnvVar",
		"defaultValue",
	}
	for _, function := range functions {
		require.Contains(t, fm, function)
	}
	for function := range fm {
		require.Contains(t, functions, function)
	}
}

func TestReceiverConfigProperties(t *testing.T) {
	fm := funcMap(component.MustNewID("otlp"))
	configProperty := fm["configProperty"].(func(...string) (string, error))
	configPropertyEnvVar := fm["configPropertyEnvVar"].(func(...string) (string, error))

	prop, err := configProperty("one", "two", "three", "<value>")
	require.NoError(t, err)
	require.Equal(t, `splunk.discovery.receivers.otlp.config.one::two::three="<value>"`, prop)

	prop, err = configProperty("invalid")
	require.EqualError(t, err, "configProperty takes key+ and value{1} arguments (minimum 2)")
	require.Empty(t, prop)

	prop, err = configPropertyEnvVar("one", "two", "three", "<value>")
	require.NoError(t, err)
	require.Equal(t, `SPLUNK_DISCOVERY_RECEIVERS_otlp_CONFIG_one_x3a__x3a_two_x3a__x3a_three="<value>"`, prop)

	prop, err = configPropertyEnvVar("invalid")
	require.EqualError(t, err, "configPropertyEnvVar takes key+ and value{1} arguments (minimum 2)")
	require.Empty(t, prop)
}

func TestExtensionConfigProperties(t *testing.T) {
	fm := funcMap(component.MustNewIDWithName("host_observer", "name"))
	configProperty := fm["configProperty"].(func(...string) (string, error))
	configPropertyEnvVar := fm["configPropertyEnvVar"].(func(...string) (string, error))

	prop, err := configProperty("one", "two", "three", "<value>")
	require.NoError(t, err)
	require.Equal(t, `splunk.discovery.extensions.host_observer/name.config.one::two::three="<value>"`, prop)

	prop, err = configProperty("invalid")
	require.EqualError(t, err, "configProperty takes key+ and value{1} arguments (minimum 2)")
	require.Empty(t, prop)

	prop, err = configPropertyEnvVar("one", "two", "three", "<value>")
	require.NoError(t, err)
	require.Equal(t, `SPLUNK_DISCOVERY_EXTENSIONS_host_x5f_observer_x2f_name_CONFIG_one_x3a__x3a_two_x3a__x3a_three="<value>"`, prop)

	prop, err = configPropertyEnvVar("invalid")
	require.EqualError(t, err, "configPropertyEnvVar takes key+ and value{1} arguments (minimum 2)")
	require.Empty(t, prop)
}

func TestDefaultValue(t *testing.T) {
	tmplt, err := template.New("").Funcs(funcMap(component.MustNewID("redis"))).Parse("{{ defaultValue }}")
	require.NoError(t, err)
	out := &bytes.Buffer{}
	require.NoError(t, tmplt.Execute(out, nil))
	require.Equal(t, "splunk.discovery.default", out.String())
}
