package console

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"agent2api/internal/accounts"
	"agent2api/internal/control"
	"agent2api/internal/providers"
)

func (h *Handler) HandleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": providers.List()})
}

func (h *Handler) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.Control.Accounts.List(r.Context(), r.URL.Query().Get("refresh") == "1")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "account_list_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": items})
	case http.MethodPost:
		var input struct {
			Name                 string `json:"name"`
			Provider             string `json:"provider"`
			Region               string `json:"region"`
			Enabled              bool   `json:"enabled"`
			MaxInFlight          int    `json:"max_inflight"`
			Priority             int    `json:"priority"`
			DropSystemPrompt     *bool  `json:"drop_system_prompt"`
			ModelRequestsEnabled *bool  `json:"model_requests_enabled"`
			WorkBuddyAutoCheckin *bool  `json:"workbuddy_auto_checkin"`
			WorkBuddyCheckinTime string `json:"workbuddy_checkin_time"`
			AutoCheckin          *bool  `json:"auto_checkin"`
			CheckinTime          string `json:"checkin_time"`
			ProxyURL             string `json:"proxy_url"`
			ReserveCredits       *int64 `json:"reserve_credits"`
			DailyTokenLimit      *int64 `json:"daily_token_limit"`
			DailyCreditLimit     *int64 `json:"daily_credit_limit"`
			DailyModelTokenLimit *int64 `json:"daily_model_token_limit"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConsoleBodyBytes)).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if _, _, err := providers.Resolve(input.Provider, input.Region); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_provider", err.Error())
			return
		}
		account, err := h.Control.Accounts.Create(r.Context(), accounts.CreateAccount{
			AutoCheckin: input.AutoCheckin, CheckinTime: input.CheckinTime,
			Name: input.Name, Provider: input.Provider, Region: input.Region,
			Enabled: input.Enabled, MaxInFlight: input.MaxInFlight, Priority: input.Priority,
			DropSystemPrompt: input.DropSystemPrompt, ModelRequestsEnabled: input.ModelRequestsEnabled,
			WorkBuddyAutoCheckin: input.WorkBuddyAutoCheckin,
			WorkBuddyCheckinTime: input.WorkBuddyCheckinTime, ProxyURL: input.ProxyURL,
			ReserveCredits: input.ReserveCredits, DailyTokenLimit: input.DailyTokenLimit,
			DailyCreditLimit: input.DailyCreditLimit, DailyModelTokenLimit: input.DailyModelTokenLimit,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "account_create_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, account)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST only")
	}
}

func (h *Handler) HandleAccountImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConsoleBodyBytes))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var input control.AccountImportInput
	if err = json.Unmarshal(raw, &input); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	account, err := h.Control.Accounts.Import(r.Context(), input, raw)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

// maxImportBatchItems 限制单个批次的大小：请求体上限（
// maxImportBatchBodyBytes）已经限制了体积，这里只是避免单个请求
// 独占导入路径。
const maxImportBatchItems = 100

// maxImportBatchBodyBytes 是批量导入的请求体上限。单批最多
// maxImportBatchItems 项、单条凭证通常远小于 100KB，8MiB 为整批
// 留出充足余量，同时把单请求读入内存的上界钉死——读入路径必须
// 与单条导入/其它控制台端点一样受 http.MaxBytesReader 约束。
const maxImportBatchBodyBytes = 8 << 20

// HandleAccountImportBatch 在一次调用中导入多个账号载荷，并返回逐项报告
// （imported / skipped / error）。整个请求层面的失败仍返回 4xx；单项失败属于
// 200 报告的一部分，符合轻量级批量的约定（无预览、无断点续传、逐项给出原因）。
func (h *Handler) HandleAccountImportBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBatchBodyBytes))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var payload struct {
		Items []json.RawMessage `json:"items"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(payload.Items) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_request", "items must contain at least one account")
		return
	}
	if len(payload.Items) > maxImportBatchItems {
		writeErr(w, http.StatusBadRequest, "invalid_request", "items must contain at most 100 accounts")
		return
	}
	results := h.Control.Accounts.ImportBatch(r.Context(), payload.Items)
	counts := map[string]int{}
	for _, item := range results {
		counts[item.Status]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":  results,
		"imported": counts[control.ImportBatchStatusImported],
		"skipped":  counts[control.ImportBatchStatusSkipped],
		"errors":   counts[control.ImportBatchStatusError],
	})
}

func (h *Handler) HandleAccountByID(w http.ResponseWriter, r *http.Request) {
	relative := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/accounts/"), "/")
	parts := strings.Split(relative, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, "account_not_found", "account id required")
		return
	}
	accountID := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			account, err := h.Control.Accounts.Get(r.Context(), accountID)
			if err != nil {
				writeErr(w, http.StatusNotFound, "account_not_found", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, account)
		case http.MethodPatch:
			var input struct {
				Name                 string  `json:"name"`
				Enabled              *bool   `json:"enabled"`
				MaxInFlight          *int    `json:"max_inflight"`
				Priority             *int    `json:"priority"`
				DropSystemPrompt     *bool   `json:"drop_system_prompt"`
				ModelRequestsEnabled *bool   `json:"model_requests_enabled"`
				WorkBuddyAutoCheckin *bool   `json:"workbuddy_auto_checkin"`
				WorkBuddyCheckinTime *string `json:"workbuddy_checkin_time"`
				AutoCheckin          *bool   `json:"auto_checkin"`
				CheckinTime          *string `json:"checkin_time"`
				ProxyURL             *string `json:"proxy_url"`
				ReserveCredits       *int64  `json:"reserve_credits"`
				DailyTokenLimit      *int64  `json:"daily_token_limit"`
				DailyCreditLimit     *int64  `json:"daily_credit_limit"`
				DailyModelTokenLimit *int64  `json:"daily_model_token_limit"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConsoleBodyBytes)).Decode(&input); err != nil {
				writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			account, err := h.Control.Accounts.Update(r.Context(), accountID, accounts.UpdateAccount{
				AutoCheckin: input.AutoCheckin, CheckinTime: input.CheckinTime,
				Name: input.Name, Enabled: input.Enabled, MaxInFlight: input.MaxInFlight, Priority: input.Priority,
				DropSystemPrompt: input.DropSystemPrompt, ModelRequestsEnabled: input.ModelRequestsEnabled,
				WorkBuddyAutoCheckin: input.WorkBuddyAutoCheckin,
				WorkBuddyCheckinTime: input.WorkBuddyCheckinTime, ProxyURL: input.ProxyURL,
				ReserveCredits: input.ReserveCredits, DailyTokenLimit: input.DailyTokenLimit,
				DailyCreditLimit: input.DailyCreditLimit, DailyModelTokenLimit: input.DailyModelTokenLimit,
			})
			if err != nil {
				writeErr(w, http.StatusBadRequest, "account_update_failed", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, account)
		case http.MethodDelete:
			if err := h.Control.Accounts.Delete(r.Context(), accountID); err != nil {
				writeErr(w, http.StatusBadRequest, "account_delete_failed", err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET, PATCH or DELETE only")
		}
		return
	}

	action := strings.Join(parts[1:], "/")
	if action == "refresh" {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
			return
		}
		if _, err := h.Control.Accounts.GetStored(r.Context(), accountID); err != nil {
			writeErr(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		if err := h.Control.Accounts.RefreshAccount(r.Context(), accountID, r.URL.Query().Get("quota") == "1"); err != nil {
			writeErr(w, http.StatusBadGateway, "account_refresh_failed", err.Error())
			return
		}
		view, err := h.Control.Accounts.Get(r.Context(), accountID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "account_view_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	required := map[string]string{
		"checkins":        http.MethodGet,
		"checkin":         http.MethodPost,
		"cooldowns":       http.MethodGet,
		"cooldowns/clear": http.MethodPost,
		"login/device":    http.MethodPost,
		"login/status":    http.MethodGet,
		"login/callback":  http.MethodPost,
	}
	if want, ok := required[action]; ok && r.Method != want {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", want+" only")
		return
	}
	if action == "export" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
			return
		}
		exported, err := h.Control.Accounts.Export(r.Context(), accountID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "credential_not_found", err.Error())
			return
		}
		payload := map[string]any{
			"format": exported.Format, "name": exported.Name,
			"provider": exported.Provider, "region": exported.Region,
			"credential": json.RawMessage(exported.Credential),
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	var callbackURL string
	if action == "login/callback" {
		var input struct {
			CallbackURL string `json:"callback_url"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConsoleBodyBytes)).Decode(&input); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		callbackURL = input.CallbackURL
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConsoleBodyBytes))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := h.Control.Accounts.Admin(r.Context(), control.AccountAdminAction{
		AccountID: accountID, Action: action, Method: r.Method, ContentType: r.Header.Get("Content-Type"), Body: body, CallbackURL: callbackURL,
	})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	switch result.Kind {
	case "checkins":
		records, err := h.Control.Accounts.ListCheckins(r.Context(), accountID, 20)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "checkin_list_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": records})
	case "checkin":
		updated, err := h.Control.Accounts.Checkin(r.Context(), accountID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "checkin_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case "cooldowns":
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": result.Cooldowns})
	case "cooldowns_clear":
		writeJSON(w, http.StatusOK, map[string]any{"cleared": result.Cleared})
	case "login_start":
		writeJSON(w, http.StatusOK, map[string]any{"authUrl": result.Session.AuthURL, "status": result.LoginStatus})
	case "login_status":
		writeJSON(w, http.StatusOK, map[string]any{"login": map[string]any{"status": result.LoginStatus, "message": result.LoginMsg}})
	case "login_complete":
		writeJSON(w, http.StatusOK, map[string]any{"login": map[string]any{"status": result.LoginStatus, "message": result.LoginMsg}})
	default:
		writeErr(w, http.StatusNotFound, "not_found", "unknown account action")
	}
}
