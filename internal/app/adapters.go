package app

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"qka/internal/adapterhost"
	"qka/internal/adapterwiring"
	"qka/internal/capabilities"
	"qka/internal/registry"
)

// launchAdapters 发现 AdaptersRoot 下的适配器目录并逐个启动。
// 缺 manifest 的目录（如 _template、node_modules）跳过；单个适配器启动失败不拖垮其余。
func (a *App) launchAdapters(ctx context.Context) error {
	entries, err := os.ReadDir(a.cfg.AdaptersRoot)
	if err != nil {
		return err
	}

	var dirs []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && !strings.HasPrefix(name, "_") && name != "node_modules" {
			dirs = append(dirs, filepath.Join(a.cfg.AdaptersRoot, name))
		}
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		manifest, err := adapterhost.LoadManifest(dir)
		if err != nil {
			continue // 不是有效适配器目录，跳过
		}
		loaded := Loaded{Manifest: manifest}
		host, err := a.launchOne(ctx, dir, manifest)
		if err != nil {
			loaded.LoadErr = err
		} else {
			loaded.Host = host
			a.hosts[manifest.ID] = host
		}
		a.loaded = append(a.loaded, loaded)
	}
	return nil
}

// launchOne 为单个适配器装配反向钩子（http.fetch / log.write）并完成握手。
func (a *App) launchOne(ctx context.Context, dir string, manifest adapterhost.Manifest) (*adapterhost.Host, error) {
	mux := adapterwiring.NewHookMux()

	baseURL := a.cfg.BaseURLs[manifest.ID]
	if baseURL != "" {
		mux.Init["baseURL"] = baseURL
	}

	// 出站白名单：显式 AllowedHosts + BaseURLs 的主机。
	hosts := append([]string{}, a.cfg.AllowedHosts...)
	if baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	allowed := capabilities.AllowedHostsFromManifest(hosts)
	grant := capabilities.GrantFromNeeds(manifest.Needs)
	fetch := capabilities.NewHTTPFetch(allowed, grant)

	adapterwiring.RegisterHTTPFetch(mux, fetch)
	adapterwiring.RegisterLogWrite(mux, a.store.DB())

	return adapterhost.LaunchWith(ctx, a.cfg.BunPath, dir, mux)
}

// registryAdapters 把加载结果转为 registry 视图输入；启动失败的适配器标记为禁用。
func (a *App) registryAdapters() []registry.LoadedAdapter {
	out := make([]registry.LoadedAdapter, 0, len(a.loaded))
	for _, l := range a.loaded {
		la := registry.LoadedAdapter{Manifest: l.Manifest}
		if l.Host == nil {
			la.Disabled = true
		}
		out = append(out, la)
	}
	return out
}

// capabilitiesFor 从适配器声明的能力推导目标操作（不硬编码产品清单）。
func (a *App) capabilitiesFor(target string) []string {
	ops := []string{"checkin"}
	for _, l := range a.loaded {
		if l.Manifest.ID != target {
			continue
		}
		for _, c := range l.Manifest.Capabilities {
			if c == "keepalive" {
				ops = append(ops, "keepalive")
			}
		}
	}
	return ops
}
