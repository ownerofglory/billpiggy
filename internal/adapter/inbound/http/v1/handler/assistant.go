package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
	"github.com/ownerofglory/billpiggy/pkg/sse"
)

// RegisterAssistantRoutes mounts the authenticated streaming assistant endpoint.
func RegisterAssistantRoutes(router chi.Router, assistant *service.AssistantService, middleware *sharedauth.Middleware) {
	router.Route(basePathV1+"/assistant", func(routes chi.Router) {
		routes.With(middleware.RequireAuthentication).Post("/chat", assistantHandler{service: assistant}.chat)
	})
}

type assistantHandler struct{ service *service.AssistantService }
type assistantRequest struct {
	Message string `json:"message"`
}

// streamAssistantUnavailable provides the stable SSE contract until a provider is configured.
//
//	@Summary	Stream an assistant response
//	@Tags		assistant
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		request	body		assistantRequest	true	"Assistant message"
//	@Success	200		{string}	string				"SSE events: message.started, message.delta, message.completed, message.error"
//	@Failure	401		{object}	map[string]string
//	@Router		/billpiggy/api/v1/assistant/chat [post]
func (h assistantHandler) chat(w http.ResponseWriter, r *http.Request) {
	sse.Prepare(w)
	_ = sse.Write(w, "message.started", map[string]string{"status": "started"})
	if h.service == nil {
		_ = sse.Write(w, "message.error", map[string]string{"code": "assistant_not_configured", "message": "Assistant provider is not configured"})
		return
	}
	var request assistantRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	answer, err := h.service.Ask(r.Context(), identity.Subject, request.Message)
	if errors.Is(err, service.ErrForbidden) {
		_ = sse.Write(w, "message.error", map[string]string{"code": "rate_limited", "message": "Assistant request limit reached"})
		return
	}
	if err != nil {
		_ = sse.Write(w, "message.error", map[string]string{"code": "assistant_failed", "message": "Assistant could not answer"})
		return
	}
	_ = sse.Write(w, "message.delta", map[string]string{"delta": answer})
	_ = sse.Write(w, "message.completed", map[string]string{"status": "completed"})
}
