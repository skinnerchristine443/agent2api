package control

import (
	"context"
	"encoding/json"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

type AccountImportInput struct {
	Format               string          `json:"format"`
	Name                 string          `json:"name"`
	Provider             string          `json:"provider"`
	Region               string          `json:"region"`
	Enabled              bool            `json:"enabled"`
	MaxInFlight          int             `json:"max_inflight"`
	Priority             int             `json:"priority"`
	DropSystemPrompt     *bool           `json:"drop_system_prompt"`
	WorkBuddyAutoCheckin *bool           `json:"workbuddy_auto_checkin"`
	WorkBuddyCheckinTime string          `json:"workbuddy_checkin_time"`
	AutoCheckin          *bool           `json:"auto_checkin"`
	CheckinTime          string          `json:"checkin_time"`
	ProxyURL             string          `json:"proxy_url"`
	Credential           json.RawMessage `json:"credential"`
	ReserveCredits       *int64          `json:"reserve_credits"`
	DailyTokenLimit      *int64          `json:"daily_token_limit"`
	DailyCreditLimit     *int64          `json:"daily_credit_limit"`
	DailyModelTokenLimit *int64          `json:"daily_model_token_limit"`
}

func (a *Accounts) Import(ctx context.Context, input AccountImportInput, raw []byte) (accounts.Account, error) {
	payload := input.Credential
	if len(payload) == 0 {
		payload = raw
	}
	for _, descriptor := range providers.List() {
		adapter, ok := a.Providers.Get(descriptor.ID)
		if !ok {
			continue
		}
		importer, ok := adapter.Credential.(providers.CredentialImporter)
		if !ok || importer.Format() != input.Format {
			continue
		}
		prepared, err := importer.PrepareImport(payload)
		if err != nil {
			return accounts.Account{}, operationError("invalid_credential", err.Error())
		}
		account, err := a.ImportCredentialPayload(ctx, accounts.CreateAccount{
			AutoCheckin: input.AutoCheckin, CheckinTime: input.CheckinTime,
			Name: input.Name, Provider: descriptor.ID, Region: input.Region,
			MaxInFlight: input.MaxInFlight, Priority: input.Priority, DropSystemPrompt: input.DropSystemPrompt,
			WorkBuddyAutoCheckin: input.WorkBuddyAutoCheckin, WorkBuddyCheckinTime: input.WorkBuddyCheckinTime, ProxyURL: input.ProxyURL,
			ReserveCredits: input.ReserveCredits, DailyTokenLimit: input.DailyTokenLimit,
			DailyCreditLimit: input.DailyCreditLimit, DailyModelTokenLimit: input.DailyModelTokenLimit,
		}, input.Format, prepared.Payload, prepared.Ready && input.Enabled)
		if err != nil {
			return account, operationError("account_import_failed", err.Error())
		}
		return account, nil
	}
	return accounts.Account{}, operationError("unsupported_format", "unsupported account import format")
}

// ImportBatch 的各项状态，逐项报告中每项对应一个。
const (
	ImportBatchStatusImported = "imported"
	ImportBatchStatusSkipped  = "skipped"
	ImportBatchStatusError    = "error"
)

// ImportBatchItemResult 是批量导入中单项的结果。
type ImportBatchItemResult struct {
	Index     int    `json:"index"`
	Status    string `json:"status"`
	AccountID string `json:"account_id,omitempty"`
	Name      string `json:"name,omitempty"`
	// Error 携带被跳过或失败项的原因。
	Error string `json:"error,omitempty"`
}

// ImportBatch 在一次调用中导入多个载荷，并独立报告每一项 —— 这是轻量级批量：
// 没有预览向导，也没有断点续传。重复项会被跳过而非失败：重新导入同一份凭据只会
// 创建一个幽灵副本，因此材料已存在（在已存账号中或同批次更早处）的项会被报告为跳过。
func (a *Accounts) ImportBatch(ctx context.Context, items []json.RawMessage) []ImportBatchItemResult {
	results := make([]ImportBatchItemResult, 0, len(items))
	seen := map[string]bool{}
	existing := a.existingCredentialFingerprints(ctx)
	for index, raw := range items {
		result := ImportBatchItemResult{Index: index}
		var input AccountImportInput
		if err := json.Unmarshal(raw, &input); err != nil {
			result.Status = ImportBatchStatusError
			result.Error = "invalid item: " + err.Error()
			results = append(results, result)
			continue
		}
		result.Name = input.Name
		fingerprint := importCredentialFingerprint(input, raw)
		if fingerprint != "" && (existing[fingerprint] || seen[fingerprint]) {
			result.Status = ImportBatchStatusSkipped
			result.Error = "duplicate credential already present"
			results = append(results, result)
			continue
		}
		account, err := a.Import(ctx, input, raw)
		if err != nil {
			result.Status = ImportBatchStatusError
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		if fingerprint != "" {
			seen[fingerprint] = true
			existing[fingerprint] = true
		}
		result.Status = ImportBatchStatusImported
		result.AccountID = account.ID
		result.Name = account.Name
		results = append(results, result)
	}
	return results
}

// importCredentialFingerprint 渲染一次导入将会持久化的凭据材料，用于重复检测。
// 为空表示"无法计算"，此时导入总是执行。
func importCredentialFingerprint(input AccountImportInput, raw []byte) string {
	if input.Format == "" {
		return ""
	}
	payload := input.Credential
	if len(payload) == 0 {
		payload = raw
	}
	if len(payload) == 0 {
		return ""
	}
	return input.Format + "|" + input.Provider + "|" + string(payload)
}

// existingCredentialFingerprints 为每个已存凭据生成指纹，使与现有账号重复的
// 批量项能被识别出来。
func (a *Accounts) existingCredentialFingerprints(ctx context.Context) map[string]bool {
	found := map[string]bool{}
	list, err := a.store().List(ctx)
	if err != nil {
		return found
	}
	for _, account := range list {
		if format, payload, err := a.store().LoadCredentialPayload(ctx, account.ID); err == nil && format != "" && len(payload) > 0 {
			found[format+"|"+account.Provider+"|"+string(payload)] = true
		}
	}
	return found
}
