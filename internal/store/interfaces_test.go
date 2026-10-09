package store

import (
	"agent2api/internal/accounts"
	"agent2api/internal/logs"
	"agent2api/internal/providers/trae"
	"agent2api/internal/providers/workbuddy"
)

var _ accounts.AccountStore = (*Store)(nil)
var _ accounts.PoolStateStore = (*Store)(nil)
var _ logs.RequestStore = (*Store)(nil)
var _ logs.RequestPersister = (*Store)(nil)
var _ logs.RequestQuery = (*Store)(nil)
var _ workbuddy.Store = (*Store)(nil)
var _ workbuddy.SecretReader = (*Store)(nil)
var _ workbuddy.ModelSettingReader = (*Store)(nil)
var _ trae.Store = (*Store)(nil)
var _ trae.SecretReader = (*Store)(nil)
var _ trae.ModelSettingReader = (*Store)(nil)
var _ trae.ModelMaxModeReader = (*Store)(nil)
