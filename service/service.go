/*
   Copyright The Soci Snapshotter Authors.

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

/*
   Copyright The containerd Authors.

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

package service

import (
	"context"
	"path/filepath"

	"github.com/awslabs/soci-snapshotter/config"
	socifs "github.com/awslabs/soci-snapshotter/fs"
	"github.com/awslabs/soci-snapshotter/fs/layer"
	"github.com/awslabs/soci-snapshotter/fs/source"
	"github.com/awslabs/soci-snapshotter/service/resolver"
	snbase "github.com/awslabs/soci-snapshotter/snapshot"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/overlay/overlayutils"
	"github.com/containerd/log"
)

type Option func(*options)

type options struct {
	credsFuncs    []resolver.Credential
	registryHosts resolver.RegistryHosts
	fsOpts        []socifs.Option
}

// WithCredsFuncs specifies credsFuncs to be used for connecting to the registries.
func WithCredsFuncs(creds ...resolver.Credential) Option {
	return func(o *options) {
		o.credsFuncs = append(o.credsFuncs, creds...)
	}
}

// WithCustomRegistryHosts is registry hosts to use instead.
func WithCustomRegistryHosts(hosts resolver.RegistryHosts) Option {
	return func(o *options) {
		o.registryHosts = hosts
	}
}

// WithFilesystemOptions allows to pass filesystem-related configuration.
func WithFilesystemOptions(opts ...socifs.Option) Option {
	return func(o *options) {
		o.fsOpts = opts
	}
}

// NewSociSnapshotterService returns soci snapshotter.
func NewSociSnapshotterService(ctx context.Context, root string, serviceCfg *config.ServiceConfig, opts ...Option) (snapshots.Snapshotter, error) {
	var sOpts options
	for _, o := range opts {
		o(&sOpts)
	}

	httpConfig := serviceCfg.FSConfig.RetryableHTTPClientConfig
	registryConfig := serviceCfg.RegistryConfig // Containerd-standard registry config
	resolverConfig := serviceCfg.ResolverConfig // Legacy SOCI resolver config

	hosts := sOpts.registryHosts
	if hosts == nil {
		// Default to containerd's standard certs.d directory approach.
		// This ensures parallel pulling works with TLS the same way containerd does.
		configPath := registryConfig.ConfigPath
		if configPath == "" {
			configPath = "/etc/containerd/certs.d"
		}

		criRegistry := resolver.Registry{
			ConfigPath: configPath,
		}
		hosts = resolver.RegistryHostsFromCRIConfig(ctx, criRegistry, sOpts.credsFuncs...)

		// Only fall back to legacy resolver if explicitly configured with per-host settings
		// and no certs.d path was specified
		if len(resolverConfig.Host) > 0 && registryConfig.ConfigPath == "" {
			hosts = resolver.NewRegistryManager(httpConfig, resolverConfig, sOpts.credsFuncs).AsRegistryHosts()
		}
	}
	userxattr, err := overlayutils.NeedsUserXAttr(snapshotterRoot(root))
	if err != nil {
		log.G(ctx).WithError(err).Warnf("cannot detect whether \"userxattr\" option needs to be used, assuming to be %v", userxattr)
	}
	opq := layer.OverlayOpaqueTrusted
	if userxattr {
		opq = layer.OverlayOpaqueUser
	}
	// Configure filesystem and snapshotter
	getSources := source.FromDefaultLabels(source.RegistryHosts(hosts)) // provides source info based on default labels
	fsOpts := append(sOpts.fsOpts, socifs.WithGetSources(getSources),
		socifs.WithOverlayOpaqueType(opq),
		socifs.WithPullModes(serviceCfg.PullModes),
	)
	if serviceCfg.FSConfig.MaxConcurrency != 0 {
		fsOpts = append(fsOpts, socifs.WithMaxConcurrency(serviceCfg.FSConfig.MaxConcurrency))
	}
	fs, err := socifs.NewFilesystem(ctx, fsRoot(root), serviceCfg.FSConfig, fsOpts...)
	if err != nil {
		log.G(ctx).WithError(err).Fatalf("failed to configure filesystem")
	}

	var snapshotter snapshots.Snapshotter

	snOpts := []snbase.Opt{snbase.WithAsynchronousRemove}
	if serviceCfg.MinLayerSize > -1 {
		snOpts = append(snOpts, snbase.WithMinLayerSize(serviceCfg.MinLayerSize))
	}
	if serviceCfg.SnapshotterConfig.AllowInvalidMountsOnRestart {
		snOpts = append(snOpts, snbase.AllowInvalidMountsOnRestart)
	}
	if serviceCfg.PullModes.Parallel.Enable {
		snOpts = append(snOpts, snbase.ParallelPullUnpack)
	}

	snapshotter, err = snbase.NewSnapshotter(ctx, snapshotterRoot(root), fs, snOpts...)
	if err != nil {
		log.G(ctx).WithError(err).Fatalf("failed to create new snapshotter")
	}

	return snapshotter, err
}

func snapshotterRoot(root string) string {
	return filepath.Join(root, "snapshotter")
}

func fsRoot(root string) string {
	return filepath.Join(root, "soci")
}

// Supported returns nil when the remote snapshotter is functional on the system with the root directory.
// Supported is not called during plugin initialization, but exposed for downstream projects which uses
// this snapshotter as a library.
func Supported(root string) error {
	// Remote snapshotter is implemented based on overlayfs snapshotter.
	return overlayutils.Supported(snapshotterRoot(root))
}
