package handler

import (
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

const maxUploadBytes = 10 << 20

// RegisterUploadRoutes mounts owner-scoped receipt and profile-image uploads.
func RegisterUploadRoutes(router chi.Router, auth *service.AuthService, expenses *service.ExpenseService, objects outbound.ObjectStore, middleware *sharedauth.Middleware) {
	h := uploadHandler{auth: auth, expenses: expenses, objects: objects}
	router.Route(basePathV1, func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Post("/users/me/profile-image", h.profileImage)
		routes.Post("/expenses/{expenseID}/receipt", h.receipt)
	})
}

type uploadHandler struct {
	auth     *service.AuthService
	expenses *service.ExpenseService
	objects  outbound.ObjectStore
}

func (h uploadHandler) owner(r *http.Request) string {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return identity.Subject
}

// profileImage uploads an image for the current user's profile.
//
//	@Summary	Upload profile image
//	@Tags		users
//	@Accept		multipart/form-data
//	@Produce	json
//	@Param		file	formData	file	true	"Image file, maximum 10 MiB"
//	@Success	200		{object}	userResponseBody
//	@Router		/billpiggy/api/v1/users/me/profile-image [post]
func (h uploadHandler) profileImage(w http.ResponseWriter, r *http.Request) {
	key, contentType, body, size, ok := uploadFile(w, r, true)
	if !ok {
		return
	}
	defer body.Close()
	owner := h.owner(r)
	objectKey := "profiles/" + owner + "/" + uuid.NewString() + path.Ext(key)
	if err := h.objects.Put(r.Context(), objectKey, body, size, contentType); err != nil {
		writeJSONError(w, 502, "could not store profile image")
		return
	}
	user, err := h.auth.UpdateProfileImage(r.Context(), owner, objectKey)
	if err != nil {
		writeJSONError(w, 400, "could not update profile image")
		return
	}
	writeJSON(w, 200, userPublic(user))
}

// receipt uploads an image or PDF receipt for an expense.
//
//	@Summary	Upload expense receipt
//	@Tags		expenses
//	@Accept		multipart/form-data
//	@Produce	json
//	@Param		expenseID	path		string	true	"Expense ID"
//	@Param		file		formData	file	true	"Receipt image or PDF, maximum 10 MiB"
//	@Success	200			{object}	domain.ExpenseRecord
//	@Router		/billpiggy/api/v1/expenses/{expenseID}/receipt [post]
func (h uploadHandler) receipt(w http.ResponseWriter, r *http.Request) {
	name, contentType, body, size, ok := uploadFile(w, r, false)
	if !ok {
		return
	}
	defer body.Close()
	owner := h.owner(r)
	id := chi.URLParam(r, "expenseID")
	objectKey := "receipts/" + owner + "/" + id + "/" + uuid.NewString() + path.Ext(name)
	if err := h.objects.Put(r.Context(), objectKey, body, size, contentType); err != nil {
		writeJSONError(w, 502, "could not store receipt")
		return
	}
	expense, err := h.expenses.AttachReceipt(r.Context(), owner, id, objectKey)
	if err != nil {
		writeJSONError(w, 404, "expense not found")
		return
	}
	writeJSON(w, 200, expense)
}

func uploadFile(w http.ResponseWriter, r *http.Request, imageOnly bool) (string, string, io.ReadCloser, int64, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSONError(w, 400, "invalid or oversized upload")
		return "", "", nil, 0, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, 400, "file is required")
		return "", "", nil, 0, false
	}
	contentType := header.Header.Get("Content-Type")
	valid := strings.HasPrefix(contentType, "image/")
	if !imageOnly {
		valid = valid || contentType == "application/pdf"
	}
	if !valid {
		file.Close()
		writeJSONError(w, 400, "unsupported file type")
		return "", "", nil, 0, false
	}
	return header.Filename, contentType, file, header.Size, true
}
