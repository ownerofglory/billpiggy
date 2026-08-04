package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/service"
)

// uploadHarness wires the upload/download routes behind real authentication
// with tracked object storage, and returns everything a test needs to seed
// data and inspect what was stored.
type uploadHarness struct {
	router     chi.Router
	token      string
	ownerID    string
	objects    *memory.ObjectStore
	objectRefs *memory.ObjectReferenceRepository
	expenses   *service.ExpenseService
}

func newUploadHarness(t *testing.T) uploadHarness {
	t.Helper()
	identity := memory.NewIdentityRepository()
	authService, err := service.NewAuthService(identity, service.AuthConfig{
		JWTSecret: "01234567890123456789012345678901", BootstrapSuperAdminEmail: "admin@example.com", BootstrapSuperAdminPassword: "super-admin-password",
	})
	if err != nil {
		t.Fatalf("build auth service: %v", err)
	}
	objectRefs := memory.NewObjectReferenceRepository()
	authService = authService.WithObjectReferences(objectRefs)
	if err := authService.EnsureBootstrapSuperAdmin(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	session, err := authService.Login(context.Background(), "admin@example.com", "super-admin-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	expenseRepository := memory.NewExpenseRepository()
	events := memory.NewEventStore()
	unit := memory.NewUnitOfWork(expenseRepository, objectRefs, events)
	events.WithUnitOfWork(unit)
	expenseService, err := service.NewExpenseService(expenseRepository, events, unit)
	if err != nil {
		t.Fatalf("build expense service: %v", err)
	}
	expenseService = expenseService.WithObjectReferences(objectRefs)

	objects := memory.NewObjectStore()
	router := chi.NewRouter()
	handler.RegisterUploadRoutes(router, authService, expenseService, objects, handler.NewAuthMiddleware(authService))
	handler.RegisterExpenseRoutes(router, expenseService, handler.NewAuthMiddleware(authService))

	claims, _ := authService.AuthenticateAccessToken(context.Background(), session.AccessToken)
	return uploadHarness{
		router: router, token: session.AccessToken, ownerID: claims.ID,
		objects: objects, objectRefs: objectRefs, expenses: expenseService,
	}
}

// pngFixture builds a small valid PNG.
func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x * 4 % 256), G: uint8(y * 4 % 256), B: 180, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, source); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buffer.Bytes()
}

// multipartUpload builds a multipart/form-data request carrying one file.
func multipartUpload(t *testing.T, method, url, fieldName, filename, contentType string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="` + fieldName + `"; filename="` + filename + `"`}
	header["Content-Type"] = []string{contentType}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequest(method, url, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestProfileImageUploadNormalisesAndDownloadRoundTrips(t *testing.T) {
	harness := newUploadHarness(t)

	upload := multipartUpload(t, http.MethodPost, "/billpiggy/api/v1/users/me/profile-image", "file", "avatar.png", "image/png", pngFixture(t, 40, 40))
	upload.Header.Set("Authorization", "Bearer "+harness.token)
	uploadResponse := httptest.NewRecorder()
	harness.router.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}

	// Storage must hold the normalised JPEG, not the original PNG bytes.
	keys := harness.objects.Keys()
	if len(keys) != 1 {
		t.Fatalf("stored %d objects, want 1", len(keys))
	}
	stored, err := harness.objects.Get(context.Background(), keys[0])
	if err != nil {
		t.Fatalf("get stored object: %v", err)
	}
	if stored.ContentType != "image/jpeg" {
		t.Fatalf("stored content type = %q, want image/jpeg", stored.ContentType)
	}

	download := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/users/me/profile-image", nil)
	download.Header.Set("Authorization", "Bearer "+harness.token)
	downloadResponse := httptest.NewRecorder()
	harness.router.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200 (the memory store cannot presign, so it streams)", downloadResponse.Code)
	}
	if downloadResponse.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("download content type = %q", downloadResponse.Header().Get("Content-Type"))
	}
	if downloadResponse.Body.Len() == 0 {
		t.Fatal("downloaded body is empty")
	}
}

func TestProfileImageUploadOrphansThePreviousImage(t *testing.T) {
	harness := newUploadHarness(t)

	for i := 0; i < 2; i++ {
		upload := multipartUpload(t, http.MethodPost, "/billpiggy/api/v1/users/me/profile-image", "file", "avatar.png", "image/png", pngFixture(t, 20, 20))
		upload.Header.Set("Authorization", "Bearer "+harness.token)
		response := httptest.NewRecorder()
		harness.router.ServeHTTP(response, upload)
		if response.Code != http.StatusOK {
			t.Fatalf("upload %d status = %d", i, response.Code)
		}
	}

	references := harness.objectRefs.References()
	if len(references) != 2 {
		t.Fatalf("tracked %d references, want the old and new image", len(references))
	}
	active, orphaned := 0, 0
	for _, reference := range references {
		switch reference.State {
		case domain.ObjectReferenceActive:
			active++
		case domain.ObjectReferenceOrphaned:
			orphaned++
		}
	}
	if active != 1 || orphaned != 1 {
		t.Fatalf("active=%d orphaned=%d, want exactly one of each after replacing the image", active, orphaned)
	}
}

func TestProfileImageUploadRejectsANonImage(t *testing.T) {
	harness := newUploadHarness(t)
	upload := multipartUpload(t, http.MethodPost, "/billpiggy/api/v1/users/me/profile-image", "file", "payload.sh", "image/png", []byte("#!/bin/sh\nrm -rf /\n"))
	upload.Header.Set("Authorization", "Bearer "+harness.token)
	response := httptest.NewRecorder()
	harness.router.ServeHTTP(response, upload)
	// The declared Content-Type claims an image; sniffing the actual bytes
	// must catch the mismatch regardless of what the client asserted.
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a shell script declared as image/png", response.Code)
	}
}

func TestDownloadProfileImageReports404WithoutOne(t *testing.T) {
	harness := newUploadHarness(t)
	download := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/users/me/profile-image", nil)
	download.Header.Set("Authorization", "Bearer "+harness.token)
	response := httptest.NewRecorder()
	harness.router.ServeHTTP(response, download)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestReceiptUploadNormalisesImagesAndDownloadRoundTrips(t *testing.T) {
	harness := newUploadHarness(t)
	expense, err := harness.expenses.CreateExpense(context.Background(), harness.ownerID, service.CreateExpenseCommand{
		Title: "Cinema", Currency: "EUR", CategoryID: "category-1", AmountMinor: 25_00,
		OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}

	url := "/billpiggy/api/v1/expenses/" + expense.ID + "/receipt"
	upload := multipartUpload(t, http.MethodPost, url, "file", "receipt.png", "image/png", pngFixture(t, 60, 60))
	upload.Header.Set("Authorization", "Bearer "+harness.token)
	uploadResponse := httptest.NewRecorder()
	harness.router.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var updated domain.ExpenseRecord
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.ReceiptObjectKey == "" {
		t.Fatal("expense has no receipt object key after upload")
	}

	download := httptest.NewRequest(http.MethodGet, url, nil)
	download.Header.Set("Authorization", "Bearer "+harness.token)
	downloadResponse := httptest.NewRecorder()
	harness.router.ServeHTTP(downloadResponse, download)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download status = %d", downloadResponse.Code)
	}
	if downloadResponse.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("receipt content type = %q, want the normalised image/jpeg", downloadResponse.Header().Get("Content-Type"))
	}
}

func TestReceiptUploadStoresPDFsUnmodified(t *testing.T) {
	harness := newUploadHarness(t)
	expense, err := harness.expenses.CreateExpense(context.Background(), harness.ownerID, service.CreateExpenseCommand{
		Title: "Cinema", Currency: "EUR", CategoryID: "category-1", AmountMinor: 25_00,
		OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	pdf := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF")

	url := "/billpiggy/api/v1/expenses/" + expense.ID + "/receipt"
	upload := multipartUpload(t, http.MethodPost, url, "file", "receipt.pdf", "application/pdf", pdf)
	upload.Header.Set("Authorization", "Bearer "+harness.token)
	response := httptest.NewRecorder()
	harness.router.ServeHTTP(response, upload)
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", response.Code, response.Body.String())
	}

	keys := harness.objects.Keys()
	if len(keys) != 1 {
		t.Fatalf("stored %d objects, want 1", len(keys))
	}
	stored, err := harness.objects.Get(context.Background(), keys[0])
	if err != nil {
		t.Fatalf("get stored object: %v", err)
	}
	if stored.ContentType != "application/pdf" {
		t.Fatalf("stored content type = %q, want application/pdf unmodified", stored.ContentType)
	}
	body, err := io.ReadAll(stored.Body)
	if err != nil {
		t.Fatalf("read stored body: %v", err)
	}
	if !bytes.Equal(body, pdf) {
		t.Fatal("PDF bytes were altered in storage; only images should be re-encoded")
	}
}

func TestReceiptUploadOrphansThePreviousReceipt(t *testing.T) {
	harness := newUploadHarness(t)
	expense, err := harness.expenses.CreateExpense(context.Background(), harness.ownerID, service.CreateExpenseCommand{
		Title: "Cinema", Currency: "EUR", CategoryID: "category-1", AmountMinor: 25_00,
		OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	url := "/billpiggy/api/v1/expenses/" + expense.ID + "/receipt"

	for i := 0; i < 2; i++ {
		upload := multipartUpload(t, http.MethodPost, url, "file", "receipt.png", "image/png", pngFixture(t, 30, 30))
		upload.Header.Set("Authorization", "Bearer "+harness.token)
		response := httptest.NewRecorder()
		harness.router.ServeHTTP(response, upload)
		if response.Code != http.StatusOK {
			t.Fatalf("upload %d status = %d", i, response.Code)
		}
	}

	orphaned := 0
	for _, reference := range harness.objectRefs.References() {
		if reference.State == domain.ObjectReferenceOrphaned {
			orphaned++
		}
	}
	if orphaned != 1 {
		t.Fatalf("orphaned %d references, want exactly the first receipt", orphaned)
	}
}

func TestDeletingAnExpenseOrphansItsReceipt(t *testing.T) {
	harness := newUploadHarness(t)
	expense, err := harness.expenses.CreateExpense(context.Background(), harness.ownerID, service.CreateExpenseCommand{
		Title: "Cinema", Currency: "EUR", CategoryID: "category-1", AmountMinor: 25_00,
		OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	url := "/billpiggy/api/v1/expenses/" + expense.ID + "/receipt"
	upload := multipartUpload(t, http.MethodPost, url, "file", "receipt.png", "image/png", pngFixture(t, 20, 20))
	upload.Header.Set("Authorization", "Bearer "+harness.token)
	uploadResponse := httptest.NewRecorder()
	harness.router.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("upload status = %d", uploadResponse.Code)
	}

	if err := harness.expenses.DeleteExpense(context.Background(), harness.ownerID, expense.ID); err != nil {
		t.Fatalf("delete expense: %v", err)
	}

	references := harness.objectRefs.References()
	if len(references) != 1 || references[0].State != domain.ObjectReferenceOrphaned {
		t.Fatalf("references after delete = %#v, want the receipt orphaned", references)
	}
}

func TestDownloadReceiptReports404WithoutOne(t *testing.T) {
	harness := newUploadHarness(t)
	expense, err := harness.expenses.CreateExpense(context.Background(), harness.ownerID, service.CreateExpenseCommand{
		Title: "Cinema", Currency: "EUR", CategoryID: "category-1", AmountMinor: 25_00,
		OccurredAt: time.Now().UTC(), Status: domain.ExpenseConfirmed,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	download := httptest.NewRequest(http.MethodGet, "/billpiggy/api/v1/expenses/"+expense.ID+"/receipt", nil)
	download.Header.Set("Authorization", "Bearer "+harness.token)
	response := httptest.NewRecorder()
	harness.router.ServeHTTP(response, download)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestUploadsRequireAuthentication(t *testing.T) {
	harness := newUploadHarness(t)
	upload := multipartUpload(t, http.MethodPost, "/billpiggy/api/v1/users/me/profile-image", "file", "avatar.png", "image/png", pngFixture(t, 10, 10))
	response := httptest.NewRecorder()
	harness.router.ServeHTTP(response, upload)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a token", response.Code)
	}
}
