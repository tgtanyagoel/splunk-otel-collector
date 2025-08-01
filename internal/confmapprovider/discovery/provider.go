// Copyright Splunk, Inc.
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

package discovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"go.opentelemetry.io/collector/confmap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/signalfx/splunk-otel-collector/internal/confmapprovider/discovery/properties"
)

const (
	discoveryModeScheme  = "splunk.discovery"
	propertyScheme       = "splunk.property"
	propertiesFileScheme = "splunk.properties"
	configDScheme        = "splunk.configd"
)

var _ confmap.ProviderFactory = (*providerShimFactory)(nil)
var _ confmap.Provider = (*providerShim)(nil)

type providerShimFactory struct {
	providerShim
}

func (f providerShimFactory) Create(confmap.ProviderSettings) confmap.Provider {
	return f.providerShim
}

type providerShim struct {
	retrieve func(ctx context.Context, uri string, watcher confmap.WatcherFunc) (*confmap.Retrieved, error)
	scheme   string
}

func (p providerShim) Retrieve(ctx context.Context, uri string, watcher confmap.WatcherFunc) (*confmap.Retrieved, error) {
	return p.retrieve(ctx, uri, watcher)
}

func (p providerShim) Scheme() string {
	return p.scheme
}

func (p providerShim) Shutdown(context.Context) error {
	return nil
}

type Provider struct {
	logger     *zap.Logger
	configs    map[string]*Config
	discoverer *discoverer
	retrieved  *confmap.Retrieved
}

func New() (*Provider, error) {
	m := &Provider{configs: map[string]*Config{}}
	zapConfig := zap.NewProductionConfig()
	logLevel := zap.WarnLevel
	if ll, ok := os.LookupEnv(logLevelEnvVar); ok {
		if l, err := zapcore.ParseLevel(ll); err == nil {
			logLevel = l
		}
	}
	zapConfig.Level = zap.NewAtomicLevelAt(logLevel)
	var err error
	if m.logger, err = zapConfig.Build(); err != nil {
		return (*Provider)(nil), err
	}
	if m.discoverer, err = newDiscoverer(m.logger); err != nil {
		return (*Provider)(nil), err
	}

	return m, nil
}

func (m *Provider) ConfigDProviderFactory() confmap.ProviderFactory {
	return &providerShimFactory{providerShim{
		scheme:   m.ConfigDScheme(),
		retrieve: m.retrieve(m.ConfigDScheme()),
	}}
}

func (m *Provider) DiscoveryModeProviderFactory() confmap.ProviderFactory {
	return &providerShimFactory{providerShim{
		scheme:   m.DiscoveryModeScheme(),
		retrieve: m.retrieve(m.DiscoveryModeScheme()),
	}}
}

func (m *Provider) PropertyProviderFactory() confmap.ProviderFactory {
	return &providerShimFactory{providerShim{
		scheme:   m.PropertyScheme(),
		retrieve: m.retrieve(m.PropertyScheme()),
	}}
}

func (m *Provider) PropertiesFileProviderFactory() confmap.ProviderFactory {
	return &providerShimFactory{providerShim{
		scheme:   m.PropertiesFileScheme(),
		retrieve: m.retrieve(m.PropertiesFileScheme()),
	}}
}

func (m *Provider) retrieve(scheme string) func(context.Context, string, confmap.WatcherFunc) (*confmap.Retrieved, error) {
	return func(_ context.Context, uri string, _ confmap.WatcherFunc) (*confmap.Retrieved, error) {
		schemePrefix := fmt.Sprintf("%s:", scheme)
		if !strings.HasPrefix(uri, schemePrefix) {
			return nil, fmt.Errorf("uri %q is not supported by %s provider", uri, scheme)
		}

		uriVal := uri[len(schemePrefix):]

		if schemePrefix == fmt.Sprintf("%s:", propertiesFileScheme) {
			return m.loadPropertiesFile(uriVal)
		}

		if schemePrefix == fmt.Sprintf("%s:", propertyScheme) {
			return m.parsedProperty(uriVal)
		}

		cfg, ok := m.configs[uriVal]
		if !ok {
			cfg = NewConfig(m.logger)
			cfg.propertiesAlreadyLoaded = m.discoverer.propertiesFileSpecified
			m.logger.Debug("loading config.d", zap.String("config-dir", uriVal))
			if err := cfg.Load(uriVal); err != nil {
				// ignore if we're attempting to load a default that hasn't been installed to expected path
				if uriVal == "/etc/otel/collector/config.d" && errors.Is(err, fs.ErrNotExist) {
					m.logger.Debug("failed loading default nonexistent config.d (disregarding).", zap.String("config-dir", uriVal), zap.Error(err))
					// restore empty base since fields are purged on error
					cfg = NewConfig(m.logger)
				} else {
					m.logger.Error("failed loading config.d", zap.String("config-dir", uriVal), zap.Error(err))
					return nil, err
				}
			}
			m.logger.Debug("successfully loaded config.d", zap.String("config-dir", uriVal))
			m.configs[uriVal] = cfg
		}

		if strings.HasPrefix(uri, configDScheme) {
			return confmap.NewRetrieved(cfg.toServiceConfig())
		}

		if strings.HasPrefix(uri, discoveryModeScheme) {
			// https://github.com/open-telemetry/opentelemetry-collector/pull/6833/
			// introduced repeated config resolution call so we need to memoize the provider to avoid
			// duplicate loading. TODO: expand this to be uri based for all providers
			if m.retrieved != nil {
				return m.retrieved, nil
			}
			m.logger.Debug("loading bundle.d")
			bundledCfg := NewConfig(m.logger)
			if err := bundledCfg.LoadFS(BundledFS); err != nil {
				m.logger.Error("failed loading bundle.d", zap.Error(err))
				return nil, err
			}
			m.logger.Debug("successfully loaded bundle.d")
			if err := mergeConfigWithBundle(cfg, bundledCfg); err != nil {
				return nil, fmt.Errorf("failed merging user and bundled discovery configs: %w", err)
			}
			discoveryCfg, err := m.discoverer.discover(cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to successfully discover target services: %w", err)
			}
			m.retrieved, err = confmap.NewRetrieved(discoveryCfg)
			return m.retrieved, err
		}

		return nil, fmt.Errorf("unsupported %s scheme %q", scheme, uri)
	}
}

func (m *Provider) ConfigDScheme() string {
	return configDScheme
}

func (m *Provider) DiscoveryModeScheme() string {
	return discoveryModeScheme
}

func (m *Provider) PropertyScheme() string {
	return propertyScheme
}

func (m *Provider) PropertiesFileScheme() string {
	return propertiesFileScheme
}

func (m *Provider) loadPropertiesFile(path string) (*confmap.Retrieved, error) {
	propertiesCfg := NewConfig(m.logger)
	m.logger.Debug("loading discovery properties", zap.String("file", path))
	if err := propertiesCfg.LoadProperties(path); err != nil {
		return nil, err
	}
	if err := m.discoverer.mergeDiscoveryPropertiesEntry(propertiesCfg); err != nil {
		return nil, err
	}
	m.discoverer.propertiesFileSpecified = true
	// return nil confmap to satisfy signature
	return confmap.NewRetrieved(nil)
}

func (m *Provider) parsedProperty(rawProperty string) (*confmap.Retrieved, error) {
	// split property from value
	equalsIdx := strings.Index(rawProperty, "=")
	if equalsIdx == -1 || len(rawProperty) <= equalsIdx+1 {
		return nil, fmt.Errorf("invalid discovery property %q not of form <property>=<value>", rawProperty)
	}
	prop, err := properties.NewProperty(rawProperty[:equalsIdx], rawProperty[equalsIdx+1:])
	if err != nil {
		return nil, fmt.Errorf("invalid discovery property: %w", err)
	}
	m.discoverer.propertiesConf.Merge(confmap.NewFromStringMap(prop.ToStringMap()))
	// return nil confmap to satisfy signature
	return confmap.NewRetrieved(nil)
}
