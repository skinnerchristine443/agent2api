package accounts

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// ModelRequestsDisabledSecret 以账号 ID 的 JSON 数组形式，保存被停用
// 模型流量的账号 ID。它位于 app_secrets（与 CheckinDisabledAccountsSecret 一样）
// 而非 accounts 表的列上：该开关属于账号元数据，但放在 schema 之外
// 可以避免一次迁移以及与行读取器的任何耦合。secret 缺失即表示
// 每个账号都提供模型请求，这也是默认值。
const ModelRequestsDisabledSecret = "model_requests_disabled_accounts"

// ModelRequestsReader 是停用账号集合的读取接口。
type ModelRequestsReader interface {
	GetSecret(context.Context, string) (string, bool, error)
}

// ReadModelRequestsDisabled 加载被停用模型请求的账号集合。值缺失即得到空集。
// 值畸形时故意按空集处理：该开关是 fail-open 的，因为不可读的列表
// 绝不能悄悄把所有账号都移出模型路由。
func ReadModelRequestsDisabled(ctx context.Context, store ModelRequestsReader) (map[string]struct{}, error) {
	value, found, err := store.GetSecret(ctx, ModelRequestsDisabledSecret)
	if err != nil {
		return nil, err
	}
	return ParseModelRequestsDisabled(value, found), nil
}

// ParseModelRequestsDisabled 将已存储的 JSON 数组解码为 ID 集合。
func ParseModelRequestsDisabled(value string, found bool) map[string]struct{} {
	set := map[string]struct{}{}
	if !found || strings.TrimSpace(value) == "" {
		return set
	}
	var ids []string
	if err := json.Unmarshal([]byte(value), &ids); err != nil {
		return set
	}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			set[id] = struct{}{}
		}
	}
	return set
}

// EncodeModelRequestsDisabled 将 ID 集合渲染为稳定的（已排序）JSON 数组，
// 使存储值仅在成员变化时才发生改变。
func EncodeModelRequestsDisabled(set map[string]struct{}) string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	encoded, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// ModelRequestsEnabled 报告在给定停用集合下某账号是否提供模型请求。
// 这是控制台所暴露的正向语义视图。
func ModelRequestsEnabled(set map[string]struct{}, id string) bool {
	_, disabled := set[id]
	return !disabled
}

// NewAccountView 构建一个账号视图，其模型请求开关由权威的停用集合派生而来。
// 它是唯一被认可的构造函数：ModelRequestsEnabled 是一个普通 bool，其零值为
// false（即「已停用」），因此漏填该字段的 AccountView 字面量会把
// 每个账号都报告为已停用。运行时视图构建器会调用此函数，然后再填入
// 其余仅运行时的字段（Ready/Hot/InFlight/ProxyURL/...）。
func NewAccountView(account Account, disabled map[string]struct{}) AccountView {
	return AccountView{
		Account:              account,
		Quota:                account.Quota,
		ModelRequestsEnabled: ModelRequestsEnabled(disabled, account.ID),
	}
}
