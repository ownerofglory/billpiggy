package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// maxIntakeUploadBytes bounds a scanned receipt or dictated audio file.
const maxIntakeUploadBytes = 10 << 20

// RegisterExpenseIntakeRoutes mounts the AI-assisted expense entry endpoints:
// photo/document scan, free-text "intelligent" entry, and audio dictation.
//
// None of them persist an expense. Extraction can misread a receipt or
// misparse a sentence, so every endpoint returns a draft for the client to
// show the user, who submits it through the ordinary expense-creation
// endpoint once satisfied.
//
// Routes are registered with Group rather than Route/Mount, matching
// RegisterUploadRoutes: these paths fall under the same basePathV1+"/expenses"
// prefix RegisterExpenseRoutes separately mounts a sub-router at, and two
// competing Mounts on an overlapping prefix silently 404 real requests even
// though chi.Walk shows every route registered.
func RegisterExpenseIntakeRoutes(router chi.Router, intake inbound.ExpenseIntakeService, auth inbound.AuthService, middleware *sharedauth.Middleware) {
	h := intakeHandler{service: intake, auth: auth}
	router.Group(func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Post(basePathV1+"/expenses/scan", h.scan)
		routes.Post(basePathV1+"/expenses/intelligent", h.intelligent)
		routes.Post(basePathV1+"/expenses/dictate", h.dictate)
	})
}

type intakeHandler struct {
	service inbound.ExpenseIntakeService
	auth    inbound.AuthService
}

type intelligentRequest struct {
	Text string `json:"text"`
}

type dictateResponse struct {
	Transcript string                  `json:"transcript"`
	Draft      domain.ExtractedExpense `json:"draft"`
}

func (h intakeHandler) owner(r *http.Request) string {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return identity.Subject
}

// scan extracts a draft expense from a photographed or scanned receipt.
//
//	@Summary	Scan a receipt into a draft expense
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Accept		multipart/form-data
//	@Produce	json
//	@Param		file	formData	file	true	"Receipt image, maximum 10 MiB"
//	@Success	200		{object}	domain.ExtractedExpense
//	@Failure	400		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	503		{object}	map[string]string
//	@Router		/billpiggy/api/v1/expenses/scan [post]
func (h intakeHandler) scan(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI expense entry is not configured")
		return
	}
	if !requireAIEnabled(w, r, h.auth) {
		return
	}
	data, _, ok := readUpload(w, r, true)
	if !ok {
		return
	}
	draft, err := h.service.ExtractFromReceipt(r.Context(), h.owner(r), data)
	writeIntakeResult(w, draft, err)
}

// intelligent extracts a draft expense from a free-text description.
//
//	@Summary	Parse a sentence into a draft expense
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		intelligentRequest	true	"Description"
//	@Success	200		{object}	domain.ExtractedExpense
//	@Failure	400		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	503		{object}	map[string]string
//	@Router		/billpiggy/api/v1/expenses/intelligent [post]
func (h intakeHandler) intelligent(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI expense entry is not configured")
		return
	}
	var request intelligentRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !requireAIEnabled(w, r, h.auth) {
		return
	}
	draft, err := h.service.ExtractFromSentence(r.Context(), h.owner(r), request.Text)
	writeIntakeResult(w, draft, err)
}

// dictate transcribes spoken audio and extracts a draft expense from it.
//
//	@Summary	Dictate a draft expense
//	@Tags		expenses
//	@Security	ApiKeyAuth
//	@Accept		multipart/form-data
//	@Produce	json
//	@Param		file	formData	file	true	"Audio recording, maximum 10 MiB"
//	@Success	200		{object}	dictateResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	503		{object}	map[string]string
//	@Router		/billpiggy/api/v1/expenses/dictate [post]
func (h intakeHandler) dictate(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "AI expense entry is not configured")
		return
	}
	if !requireAIEnabled(w, r, h.auth) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxIntakeUploadBytes)
	if err := r.ParseMultipartForm(maxIntakeUploadBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid or oversized upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	draft, transcript, err := h.service.ExtractFromAudio(r.Context(), h.owner(r), file, header.Filename, header.Header.Get("Content-Type"))
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dictateResponse{Transcript: transcript, Draft: draft})
}

// writeIntakeResult writes a successful draft or maps the failure.
func writeIntakeResult(w http.ResponseWriter, draft domain.ExtractedExpense, err error) {
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// writeIntakeError maps an intake failure onto an HTTP status.
func writeIntakeError(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "AI expense entry limit reached")
		return
	}
	writeJSONError(w, http.StatusBadGateway, "could not extract an expense")
}
