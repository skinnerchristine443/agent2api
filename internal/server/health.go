package server

import (
	"net/http"
	"time"

	"agent2api/internal/buildinfo"
	"agent2api/internal/endpoint"
	"agent2api/internal/providers"
)

func providerIDs() []string {
	out := make([]string, 0, len(providers.List()))
	for _, d := range providers.List() {
		out = append(out, d.ID)
	}
	return out
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                        true,
		"service":                   "agent2api",
		"providers":                 providerIDs(),
		"cross_provider_model_pool": s.crossProviderOn(),
		"chat_url":                  endpoint.ChatCompletionsPath,
		"time":                      time.Now().UTC().Format(time.RFC3339),
		"version":                   buildinfo.Version,
		"commit":                    buildinfo.Commit,
		"maintenance":               s.updater().Maintenance.Load(),
	})
}
