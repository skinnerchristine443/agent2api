package app

import "agent2api/internal/control"

type SecretStore = control.SecretStore

var EnsureProxyAPIKey = control.EnsureProxyAPIKey
var EnsureConsoleKey = control.EnsureConsoleKey
var GenerateAPIKey = control.GenerateAPIKey
