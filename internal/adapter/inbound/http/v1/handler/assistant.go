package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
	"github.com/ownerofglory/billpiggy/pkg/sse"
)

// RegisterAssistantRoutes mounts the authenticated streaming assistant endpoint.
func RegisterAssistantRoutes(router chi.Router, middleware *sharedauth.Middleware) {
	router.Route(basePathV1+"/assistant", func(routes chi.Router) {
		routes.With(middleware.RequireAuthentication).Post("/chat", streamAssistantUnavailable)
	})
}

// streamAssistantUnavailable provides the stable SSE contract until a provider is configured.
//
//	@Summary	Stream an assistant response
//	@Tags		assistant
//	@Accept		json
//	@Produce	text/event-stream
//	@Success	200	{string}	string	"SSE events: message.started, message.delta, message.completed, message.error"
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/assistant/chat [post]
func streamAssistantUnavailable(w http.ResponseWriter, r *http.Request) {
	sse.Prepare(w)
	_ = sse.Write(w, "message.started", map[string]string{"status": "started"})
	_ = sse.Write(w, "message.error", map[string]string{"code": "assistant_not_configured", "message": "Assistant provider is not configured"})
}
