package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
	"github.com/ownerofglory/billpiggy/pkg/sse"
)

// RegisterAssistantRoutes mounts the authenticated streaming assistant endpoint.
func RegisterAssistantRoutes(router chi.Router, assistant inbound.AssistantService, auth inbound.AuthService, middleware *sharedauth.Middleware) {
	router.Route(basePathV1+"/assistant", func(routes chi.Router) {
		routes.With(middleware.RequireAuthentication).Post("/chat", assistantHandler{service: assistant, auth: auth}.chat)
	})
}

type assistantHandler struct {
	service inbound.AssistantService
	auth    inbound.AuthService
}
type assistantRequest struct {
	Message string `json:"message"`
}

// chat streams an assistant answer as Server-Sent Events.
//
// Deltas are relayed as the model produces them rather than buffered into one
// event, so the client renders the answer progressively.
//
//	@Summary	Stream an assistant response
//	@Tags		assistant
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		request	body		assistantRequest	true	"Assistant message"
//	@Success	200		{string}	string				"SSE events: message.started, message.delta, message.completed, message.error"
//	@Failure	401		{object}	map[string]string
//	@Router		/billpiggy/api/v1/assistant/chat [post]
func (h assistantHandler) chat(w http.ResponseWriter, r *http.Request) {
	// The request body is decoded and the AI opt-out checked before the SSE
	// headers go out, so a malformed body or a disabled account gets an
	// ordinary JSON error rather than an event on a stream the client has
	// already committed to.
	var request assistantRequest
	if h.service != nil && !decodeJSON(w, r, &request) {
		return
	}
	if h.service != nil && !requireAIEnabled(w, r, h.auth) {
		return
	}
	sse.Prepare(w)
	_ = sse.Write(w, "message.started", map[string]string{"status": "started"})
	if h.service == nil {
		_ = sse.Write(w, "message.error", map[string]string{"code": "assistant_not_configured", "message": "Assistant provider is not configured"})
		return
	}

	identity, _ := sharedauth.IdentityFromContext(r.Context())
	chunks, err := h.service.AskStream(r.Context(), identity.Subject, request.Message)
	if err != nil {
		writeAssistantError(w, err)
		return
	}
	for chunk := range chunks {
		if chunk.Err != nil {
			_ = sse.Write(w, "message.error", map[string]string{"code": "assistant_failed", "message": "Assistant could not answer"})
			// Draining the rest keeps the provider goroutine from blocking on a
			// channel nobody reads.
			for range chunks {
			}
			return
		}
		if chunk.ContentDelta != "" {
			if err := sse.Write(w, "message.delta", map[string]string{"delta": chunk.ContentDelta}); err != nil {
				for range chunks {
				}
				return
			}
		}
		if chunk.Done {
			_ = sse.Write(w, "message.completed", map[string]string{"status": "completed"})
		}
	}
}

// writeAssistantError maps a request-time failure onto the SSE error contract.
func writeAssistantError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrForbidden) {
		_ = sse.Write(w, "message.error", map[string]string{"code": "rate_limited", "message": "Assistant request limit reached"})
		return
	}
	_ = sse.Write(w, "message.error", map[string]string{"code": "assistant_failed", "message": "Assistant could not answer"})
}
