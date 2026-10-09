package api

import (
	"context"

	"agent2api/internal/app"
)

func ensureProxyAPIKey(ctx context.Context, store app.SecretStore, bootstrap string) (string, bool, error) {
	return app.EnsureProxyAPIKey(ctx, store, bootstrap)
}
