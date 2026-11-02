package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"qka/internal/atomicfile"
	"qka/internal/capabilities"
	"qka/internal/providerruntime"
	"qka/internal/providers"
)

var (
	discoverProviderPlugins = providerruntime.DiscoverPlugins
	launchProviderRuntime   = providerruntime.Launch
)

func (a *App) providerRuntimeFactory() providers.RuntimeFactory {
	return func(ctx context.Context) (providers.Runtime, error) {
		root := a.cfg.ProviderPluginsRoot
		if root == "" {
			return nil, errors.New("provider plugins are not bundled")
		}
		plugins, err := discoverProviderPlugins(root)
		if err != nil {
			return nil, err
		}
		if len(plugins) == 0 {
			return nil, errors.New("no provider plugins were found")
		}
		authPath := providers.StaleAuthPath(a.cfg.ConfigDir)
		if err := providerruntime.RemoveAuthFiles(authPath); err != nil {
			return nil, err
		}
		auth := []byte("{}")
		if a.credentials != nil {
			credential, err := a.credentials.Resolve(ctx, capabilities.CredentialRef{
				Target: "qka-provider", AccountID: "runtime", SourceID: "auth",
				Kind: capabilities.Managed,
			})
			switch {
			case err == nil:
				if json.Valid([]byte(credential.Secret)) {
					auth = []byte(credential.Secret)
				}
			case errors.Is(err, capabilities.ErrSecretNotFound):
			default:
				return nil, err
			}
		}
		if err := atomicfile.WriteFile(authPath, auth, 0o600); err != nil {
			return nil, err
		}
		host, err := launchProviderRuntime(ctx, providerruntime.LaunchConfig{
			Bun:        a.cfg.BunPath,
			ConfigDir:  a.cfg.ConfigDir,
			AuthPath:   authPath,
			Directory:  a.cfg.ConfigDir,
			Plugins:    plugins,
			LoadMillis: 30_000,
			ListMillis: 30_000,
		})
		if err != nil {
			_ = providerruntime.RemoveAuthFiles(authPath)
			return nil, fmt.Errorf("provider runtime: %w", err)
		}
		return host, nil
	}
}
