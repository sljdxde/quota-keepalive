package gui

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPluginsViewMarksUnsignedUnverified(t *testing.T) {
	view := NewPluginsView([]PluginRow{
		{ID: "signed-a", Name: "A", Version: "1.0", Source: "market", Signed: true, Verified: true, UpdateStatus: "up_to_date"},
		{ID: "local-b", Name: "B", Version: "0.1", Source: "local", Signed: false},
	}, true, []AuditLine{{PackageName: "local-b", Action: "load_confirmed"}})

	if !view.DevMode {
		t.Error("开发者模式应开启")
	}
	if len(view.Audit) != 1 {
		t.Error("应包含受限加载审计")
	}
	var signed, unsigned *PluginRow
	for i := range view.Plugins {
		switch view.Plugins[i].ID {
		case "signed-a":
			signed = &view.Plugins[i]
		case "local-b":
			unsigned = &view.Plugins[i]
		}
	}
	if signed.Verified != true || signed.UpdateStatus != "up_to_date" {
		t.Error("已签名适配器应保持已验证")
	}
	if unsigned.Verified != false || unsigned.UpdateStatus != "unverified" {
		t.Error("未签名适配器应标注未验证且更新状态为 unverified")
	}
}

func TestBoundPluginsList(t *testing.T) {
	svc := NewBoundService(Deps{
		Plugins: func(context.Context) (PluginsView, error) {
			return NewPluginsView(nil, false, nil), nil
		},
		SetDevMode: func(bool) error { return nil },
	})
	raw, err := svc.PluginsList()
	if err != nil {
		t.Fatal(err)
	}
	var view PluginsView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDevMode(true); err != nil {
		t.Fatal(err)
	}
}
