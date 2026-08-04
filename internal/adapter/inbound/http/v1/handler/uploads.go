package handler

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
	"github.com/ownerofglory/billpiggy/pkg/imageproc"
)

const maxUploadBytes = 10 << 20

// presignTTL bounds how long a download redirect stays valid.
const presignTTL = 5 * time.Minute

// RegisterUploadRoutes mounts owner-scoped receipt and profile-image upload
// and download endpoints.
//
// It registers full literal paths on router via Group rather than mounting a
// sub-router with Route at basePathV1: two of those paths ("/expenses/...")
// fall under the same prefix RegisterExpenseRoutes mounts its own sub-router
// at, and chi's tree only dispatches requests to one mount per overlapping
// prefix. Route/Mount there silently 404s the shadowed side at request time
// even though both patterns show up in chi.Walk. Group shares the parent's
// tree instead of creating a competing mount, so there is nothing to conflict.
func RegisterUploadRoutes(router chi.Router, auth inbound.AuthService, expenses inbound.ExpenseService, objects outbound.ObjectStore, middleware *sharedauth.Middleware) {
	h := uploadHandler{auth: auth, expenses: expenses, objects: objects}
	router.Group(func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Get(basePathV1+"/users/me/profile-image", h.downloadProfileImage)
		routes.Post(basePathV1+"/users/me/profile-image", h.profileImage)
		routes.Get(basePathV1+"/expenses/{expenseID}/receipt", h.downloadReceipt)
		routes.Post(basePathV1+"/expenses/{expenseID}/receipt", h.receipt)
	})
}

type uploadHandler struct {
	auth     inbound.AuthService
	expenses inbound.ExpenseService
	objects  outbound.ObjectStore
}

func (h uploadHandler) owner(r *http.Request) string {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return identity.Subject
}

func (h uploadHandler) actor(r *http.Request) domain.AppUser {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
}

// profileImage uploads an image for the current user's profile. It is
// downscaled and re-encoded before storage, which also strips any metadata
// the original carried.
//
//	@Summary	Upload profile image
//	@Tags		users
//	@Accept		multipart/form-data
//	@Produce	json
//	@Param		file	formData	file	true	"Image file, maximum 10 MiB"
//	@Success	200		{object}	userResponseBody
//	@Router		/billpiggy/api/v1/users/me/profile-image [post]
func (h uploadHandler) profileImage(w http.ResponseWriter, r *http.Request) {
	// readUpload with imageOnly=true has already rejected anything that is not
	// a supported image, so the detected type needs no further use here.
	data, _, ok := readUpload(w, r, true)
	if !ok {
		return
	}
	result, err := imageproc.Normalize(bytes.NewReader(data), imageproc.ProfileImageOptions())
	if err != nil {
		writeJSONError(w, 400, "could not process image")
		return
	}
	owner := h.owner(r)
	objectKey := "profiles/" + owner + "/" + uuid.NewString() + ".jpg"
	if err := h.objects.Put(r.Context(), objectKey, bytes.NewReader(result.Data), int64(len(result.Data)), result.ContentType); err != nil {
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

// downloadProfileImage redirects to the current user's profile image.
//
//	@Summary	Download profile image
//	@Tags		users
//	@Success	302
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/users/me/profile-image [get]
func (h uploadHandler) downloadProfileImage(w http.ResponseWriter, r *http.Request) {
	user, err := h.auth.GetProfile(r.Context(), h.owner(r))
	if err != nil || user.ProfileImageObjectKey == "" {
		writeJSONError(w, 404, "profile image not found")
		return
	}
	serveObject(w, r, h.objects, user.ProfileImageObjectKey)
}

// receipt uploads an image or PDF receipt for an expense. Image receipts are
// downscaled, converted to grayscale and re-encoded before storage to save
// space and, once extraction reads them, tokens; PDFs are stored unmodified.
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
	data, detected, ok := readUpload(w, r, false)
	if !ok {
		return
	}
	body, contentType, extension := data, detected, extensionFor(detected)
	if imageproc.IsSupportedImage(detected) {
		result, err := imageproc.Normalize(bytes.NewReader(data), imageproc.ReceiptOptions())
		if err != nil {
			writeJSONError(w, 400, "could not process receipt image")
			return
		}
		body, contentType, extension = result.Data, result.ContentType, ".jpg"
	}
	owner := h.owner(r)
	id := chi.URLParam(r, "expenseID")
	objectKey := "receipts/" + owner + "/" + id + "/" + uuid.NewString() + extension
	if err := h.objects.Put(r.Context(), objectKey, bytes.NewReader(body), int64(len(body)), contentType); err != nil {
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

// downloadReceipt redirects to the receipt of an expense the viewer owns or
// that is shared with one of their groups.
//
//	@Summary	Download expense receipt
//	@Tags		expenses
//	@Param		expenseID	path	string	true	"Expense ID"
//	@Success	302
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/expenses/{expenseID}/receipt [get]
func (h uploadHandler) downloadReceipt(w http.ResponseWriter, r *http.Request) {
	expense, err := h.expenses.GetExpenseForViewer(r.Context(), h.actor(r), chi.URLParam(r, "expenseID"))
	if err != nil || expense.ReceiptObjectKey == "" {
		writeJSONError(w, 404, "receipt not found")
		return
	}
	serveObject(w, r, h.objects, expense.ReceiptObjectKey)
}

// serveObject redirects to a presigned URL when the store supports one, and
// falls back to streaming the object through this handler otherwise — which
// is what the in-memory store used in local development needs.
func serveObject(w http.ResponseWriter, r *http.Request, objects outbound.ObjectStore, objectKey string) {
	url, err := objects.PresignedGetURL(r.Context(), objectKey, presignTTL)
	switch {
	case err == nil:
		http.Redirect(w, r, url, http.StatusFound)
		return
	case errors.Is(err, outbound.ErrObjectNotFound):
		writeJSONError(w, 404, "object not found")
		return
	case !errors.Is(err, outbound.ErrPresignUnsupported):
		writeJSONError(w, 502, "could not access object")
		return
	}
	object, err := objects.Get(r.Context(), objectKey)
	if err != nil {
		if errors.Is(err, outbound.ErrObjectNotFound) {
			writeJSONError(w, 404, "object not found")
		} else {
			writeJSONError(w, 502, "could not access object")
		}
		return
	}
	defer object.Body.Close()
	w.Header().Set("Content-Type", object.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	_, _ = io.Copy(w, object.Body)
}

// readUpload extracts the file field, sniffs its actual content type rather
// than trusting what the client declared, and validates it. The declared
// Content-Type header is deliberately never used past this point: what the
// bytes actually are is what gets stored and served back.
func readUpload(w http.ResponseWriter, r *http.Request, imageOnly bool) ([]byte, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSONError(w, 400, "invalid or oversized upload")
		return nil, "", false
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, 400, "file is required")
		return nil, "", false
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, 400, "could not read upload")
		return nil, "", false
	}
	detected := imageproc.DetectContentType(data)
	valid := imageproc.IsSupportedImage(detected)
	if !imageOnly {
		valid = valid || detected == "application/pdf"
	}
	if !valid {
		writeJSONError(w, 400, "unsupported file type")
		return nil, "", false
	}
	return data, detected, true
}

// extensionFor returns the file extension to store a detected content type
// under. An unrecognised type stores with no extension.
func extensionFor(contentType string) string {
	switch contentType {
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}
