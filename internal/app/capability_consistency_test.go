package app_test

import (
	"testing"

	"agent2api/internal/app"
	"agent2api/internal/config"
	"agent2api/internal/providers"
)

// TestCapabilitiesMatchAdapterWiring 将控制台渲染的 descriptor 能力标志
// 与实际能提供这些能力的 adapter 方法钉在一起。
//
// 能力标志虚报会造出一个永远失败的按钮：控制台提供——甚至可能预选——一个
// 网关会以 provider_unsupported 作答的操作。这并非假设：此前确有 provider
// 宣称支持 login，却根本没有接任何 LoginSessionProvider。
//
// Chat/ModelCatalog/Login/BrowserLogin 属于 adapter 接线事实，因此直接针对
// Supports 断言。import_export 的断言方式不同——它断言的是「存在某条真实导入路径」——
// 因为其真值并不取决于 adapter 接线：导入由凭据编解码器的 CredentialImporter 提供，
// 或者，对于使用原生凭据格式的 provider，由控制面导入的专用分支提供。
//
// pat_login 刻意不做断言：LoginPAT 还要求粘贴的 {"api_key": …} 负载通过
// 编解码器的 PrepareImport，而这是负载形状的按 provider 属性，而非接口集合的属性。
func TestCapabilitiesMatchAdapterWiring(t *testing.T) {
	a, err := app.New(config.Config{Home: t.TempDir(), DataDir: t.TempDir(), RuntimeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	descriptors := providers.List()
	if len(descriptors) == 0 {
		t.Fatal("no builtin providers to check")
	}
	for _, descriptor := range descriptors {
		descriptor := descriptor
		t.Run(descriptor.ID, func(t *testing.T) {
			adapter, ok := a.Providers.Get(descriptor.ID)
			if !ok {
				t.Fatalf("descriptor %q has no registered adapter", descriptor.ID)
			}
			if adapter.ID != descriptor.ID {
				t.Fatalf("adapter reports ID %q, descriptor says %q", adapter.ID, descriptor.ID)
			}
			caps := descriptor.Capabilities
			for _, check := range []struct {
				flag string
				on   bool
				want bool
			}{
				{"chat", caps.Chat, adapter.Supports("chat")},
				{"model_catalog", caps.ModelCatalog, adapter.Supports("models")},
				{"login", caps.Login, adapter.Supports("login")},
				{"browser_login", caps.BrowserLogin, adapter.Supports("login")},
				{"growth", caps.Growth, adapter.Supports("growth")},
			} {
				if check.on != check.want {
					t.Errorf("capabilities.%s = %v, but adapter wiring says %v", check.flag, check.on, check.want)
				}
			}
			// 浏览器登录是登录能力的一种细化，绝不会独立于它存在。
			if caps.BrowserLogin && !caps.Login {
				t.Error("browser_login is set while login is not")
			}
			// 宣称支持 import 就必须有可去的落点。
			if caps.ImportExport {
				if _, isImporter := adapter.Credential.(providers.CredentialImporter); !isImporter {
					t.Error("import_export is set but the provider has no import path (no CredentialImporter)")
				}
			}
			for _, region := range descriptor.Regions {
				if region.Checkin != nil && !adapter.Supports("checkin") {
					t.Errorf("region %q advertises check-in but the adapter has no checkin implementation", region.ID)
				}
			}
		})
	}
}
