package usage

import "github.com/go-chi/chi/v5"

func RegisterRoutes(r chi.Router, h *Handler) {
	r.Get("/usage", h.getUsage)
	r.Delete("/usage", h.clearUsage)
	r.Post("/usage/import", h.importUsage)
	r.Get("/usage/settings", h.getUsageSettings)
	r.Put("/usage/settings", h.updateUsageSettings)
}
