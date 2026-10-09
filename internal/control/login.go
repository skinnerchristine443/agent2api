package control

import (
	"context"
	"encoding/json"
	"log"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

func (a *Accounts) login(ctx context.Context, id string) (providers.LoginSessionProvider, error) {
	account, err := a.GetStored(ctx, id)
	if err != nil {
		return nil, err
	}
	adapter, ok := a.Providers.Get(account.Provider)
	if !ok || !adapter.Supports("login") {
		return nil, operationError("provider_unsupported", "provider does not support this action")
	}
	return adapter.Login, nil
}
func (a *Accounts) StartLogin(ctx context.Context, id string) (providers.LoginSession, error) {
	login, err := a.login(ctx, id)
	if err != nil {
		return providers.LoginSession{}, err
	}
	session, err := login.StartLogin(ctx, id)
	if err != nil {
		return session, operationError("login_start_failed", err.Error())
	}
	return session, nil
}
func (a *Accounts) PollLogin(ctx context.Context, id string) (bool, string, error) {
	login, err := a.login(ctx, id)
	if err != nil {
		return false, "", err
	}
	done, message, err := login.PollLogin(ctx, id)
	if err != nil {
		return done, message, operationError("login_poll_failed", err.Error())
	}
	return done, message, nil
}
func (a *Accounts) CompleteLogin(ctx context.Context, id, callback string) error {
	login, err := a.login(ctx, id)
	if err != nil {
		return err
	}
	completer, ok := login.(providers.LoginCompleter)
	if !ok {
		return operationError("provider_unsupported", "provider does not accept a pasted callback URL")
	}
	// 先对已存材料做快照：adapter 会在内部写入新凭据，只有材料真实变化时才应
	// 释放账号的冷却。
	beforeFormat, beforePayload, _ := a.store().LoadCredentialPayload(ctx, id)
	if err := completer.CompleteLogin(ctx, id, callback); err != nil {
		return operationError("login_callback_failed", err.Error())
	}
	a.releaseCooldownsOnCredentialChange(ctx, id, beforeFormat, beforePayload)
	return nil
}

// releaseCooldownsOnCredentialChange 在凭据替换确实改变了已存材料（指纹规则）时
// 清除账号的冷却：新凭据使黑名单此前惩罚的认证失败失效，而重新保存相同的字节则
// 保留它。清除经由 runtime 进行，这样内存中的连接池窗口与已持久化的行一起被释放。
// 刻意设计为尽力而为 —— 凭据写入已经成功，释放失败不得让登录路径失败。
func (a *Accounts) releaseCooldownsOnCredentialChange(ctx context.Context, id, beforeFormat string, beforePayload []byte) {
	afterFormat, afterPayload, err := a.store().LoadCredentialPayload(ctx, id)
	if err != nil {
		return
	}
	if afterFormat == beforeFormat && string(afterPayload) == string(beforePayload) {
		return
	}
	if _, err := a.runtime.ClearCooldowns(ctx, id, ""); err != nil {
		log.Printf("credential change release account=%s: %v", id, err)
	}
}

// LoginPAT 为某个进程内账号存储粘贴进来的 provider 原生 token，该账号的 adapter
// 暴露了 PAT 凭据（个人访问令牌）。只有实现了 CredentialImporter 的
// adapter 才接受此操作。token 会被包装为 {"api_key": …} 交给 importer，随后以
// provider 自己的凭据格式持久化，并将账号启用并启动。
func (a *Accounts) LoginPAT(ctx context.Context, id, token string) error {
	account, err := a.GetStored(ctx, id)
	if err != nil {
		return err
	}
	adapter, ok := a.Providers.Get(account.Provider)
	if !ok || !adapter.Supports("credential") {
		return operationError("provider_unsupported", "provider does not support PAT login")
	}
	importer, ok := adapter.Credential.(providers.CredentialImporter)
	if !ok {
		return operationError("provider_unsupported", "provider does not support PAT login")
	}
	payload, err := json.Marshal(map[string]string{"api_key": token})
	if err != nil {
		return operationError("invalid_credential", err.Error())
	}
	prepared, err := importer.PrepareImport(payload)
	if err != nil {
		return operationError("invalid_credential", err.Error())
	}
	beforeFormat, beforePayload, _ := a.store().LoadCredentialPayload(ctx, id)
	if err := a.store().SaveCredentialPayload(ctx, id, importer.Format(), prepared.Payload); err != nil {
		return operationError("credential_save_failed", err.Error())
	}
	enabled := true
	if err := a.store().Update(ctx, id, accounts.UpdateAccount{Enabled: &enabled}); err != nil {
		return err
	}
	updated, err := a.store().Get(ctx, id)
	if err != nil {
		return err
	}
	if err := a.runtime.StartAccount(ctx, updated); err != nil {
		return err
	}
	a.releaseCooldownsOnCredentialChange(ctx, id, beforeFormat, beforePayload)
	return nil
}
