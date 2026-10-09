package accounts

// QuotaWindow 是一个 provider 原生的用量周期，例如每
// 日与每周的包含配额。顶层的 QuotaSnapshot 字段在路由时
// 取这些窗口中更紧的那个。
type QuotaWindow struct {
	ID         string  `json:"id"`
	Label      string  `json:"label,omitempty"`
	Used       float64 `json:"used"`
	Total      float64 `json:"total"`
	Remaining  float64 `json:"remaining"`
	Percentage float64 `json:"percentage"`
	Unit       string  `json:"unit,omitempty"`
	ResetAt    string  `json:"reset_at,omitempty"`
	Exceeded   bool    `json:"exceeded,omitempty"`
}

// QuotaPackage 是一个上游的额度/资源包，自带其过期时间。EndsAt 是 Unix 秒；
// EndTime 是 provider 原生的、用于展示的墙钟时间字符串。
type QuotaPackage struct {
	Remain  float64 `json:"remain"`
	Used    float64 `json:"used"`
	Size    float64 `json:"size"`
	Unit    string  `json:"unit,omitempty"`
	EndsAt  int64   `json:"ends_at,omitempty"`
	EndTime string  `json:"end_time,omitempty"`
}

// QuotaSnapshot 是一个账号的配额状态。
// 零值表示「未知」；只有 Exceeded 会影响路由。
type QuotaSnapshot struct {
	Used       float64       `json:"used"`
	Total      float64       `json:"total"`
	Remaining  float64       `json:"remaining"`
	Percentage float64       `json:"percentage"`
	Unit       string        `json:"unit"`
	Exceeded   bool          `json:"exceeded"`
	Windows    []QuotaWindow `json:"windows,omitempty"`
	// ExpiresAt 是最早的包过期时间（Unix 秒）；0 表示 provider 未上报。
	// ExpiringRemain 是在该时刻过期的剩余额度。
	ExpiresAt                int64          `json:"expires_at,omitempty"`
	ExpiringRemain           float64        `json:"expiring_remain,omitempty"`
	Packages                 []QuotaPackage `json:"packages,omitempty"`
	HasAddOn                 bool           `json:"has_add_on"`
	AddOnUsed                float64        `json:"add_on_used"`
	AddOnTotal               float64        `json:"add_on_total"`
	AddOnRemaining           float64        `json:"add_on_remaining"`
	AddOnUnit                string         `json:"add_on_unit"`
	AddOnAvailable           *bool          `json:"add_on_available,omitempty"`
	HasResourcePackage       bool           `json:"has_resource_package"`
	ResourcePackageUsed      float64        `json:"resource_package_used"`
	ResourcePackageTotal     float64        `json:"resource_package_total"`
	ResourcePackageRemaining float64        `json:"resource_package_remaining"`
	ResourcePackageUnit      string         `json:"resource_package_unit"`
	ResourcePackageAvailable *bool          `json:"resource_package_available,omitempty"`
	FetchedAt                string         `json:"fetched_at"`
	// Plan 是上游订阅层级。为空表示未上报。
	Plan string `json:"plan,omitempty"`
}
