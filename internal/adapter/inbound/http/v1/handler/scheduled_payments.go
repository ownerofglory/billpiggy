package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterScheduledPaymentRoutes mounts authenticated recurring payment CRUD
// endpoints.
func RegisterScheduledPaymentRoutes(router chi.Router, payments inbound.ScheduledPaymentService, middleware *sharedauth.Middleware) {
	h := scheduledPaymentHandler{service: payments}
	router.Route(basePathV1+"/scheduled-payments", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.With(permission(middleware, domain.PermissionPaymentsRead)).Get("/", h.list)
		routes.With(permission(middleware, domain.PermissionPaymentsWrite)).Post("/", h.create)
		routes.With(permission(middleware, domain.PermissionPaymentsRead)).Get("/{paymentID}", h.get)
		routes.With(permission(middleware, domain.PermissionPaymentsWrite)).Put("/{paymentID}", h.update)
		routes.With(permission(middleware, domain.PermissionPaymentsWrite)).Delete("/{paymentID}", h.delete)
	})
}

type scheduledPaymentHandler struct {
	service inbound.ScheduledPaymentService
}

// scheduledPaymentRequest is the recurring payment write model.
type scheduledPaymentRequest struct {
	Title              string                  `json:"title"`
	AmountMinor        int64                   `json:"amount_minor"`
	Currency           string                  `json:"currency"`
	CategoryID         string                  `json:"category_id"`
	CategoryName       string                  `json:"category_name"`
	TagIDs             []string                `json:"tag_ids"`
	SharedGroupID      string                  `json:"shared_group_id"`
	Frequency          domain.PaymentFrequency `json:"frequency"`
	CustomIntervalDays int                     `json:"custom_interval_days"`
	StartDate          time.Time               `json:"start_date"`
	EndDate            *time.Time              `json:"end_date"`
	AutoPost           bool                    `json:"auto_post"`
	ReminderDaysBefore int                     `json:"reminder_days_before"`
	Paused             bool                    `json:"paused"`
}

func (h scheduledPaymentHandler) actor(r *http.Request) domain.AppUser {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return domain.AppUser{ID: identity.Subject, Role: domain.UserRole(identity.Role)}
}

func (h scheduledPaymentHandler) payment(request scheduledPaymentRequest) domain.ScheduledPayment {
	return domain.ScheduledPayment{
		Title: request.Title, AmountMinor: request.AmountMinor, Currency: request.Currency,
		CategoryID: request.CategoryID, CategoryName: request.CategoryName, TagIDs: request.TagIDs,
		SharedGroupID: request.SharedGroupID, Frequency: request.Frequency,
		CustomIntervalDays: request.CustomIntervalDays, StartDate: request.StartDate, EndDate: request.EndDate,
		AutoPost: request.AutoPost, ReminderDaysBefore: request.ReminderDaysBefore, Paused: request.Paused,
	}
}

// list returns the current user's scheduled payments.
//
//	@Summary	List scheduled payments
//	@Tags		scheduled-payments
//	@Produce	json
//	@Success	200	{array}		domain.ScheduledPayment
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/scheduled-payments/ [get]
func (h scheduledPaymentHandler) list(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.ListScheduledPayments(r.Context(), h.actor(r))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not list scheduled payments")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// get returns one of the current user's scheduled payments.
//
//	@Summary	Get scheduled payment
//	@Tags		scheduled-payments
//	@Produce	json
//	@Param		paymentID	path		string	true	"Scheduled payment ID"
//	@Success	200			{object}	domain.ScheduledPayment
//	@Failure	404			{object}	map[string]string
//	@Router		/billpiggy/api/v1/scheduled-payments/{paymentID} [get]
func (h scheduledPaymentHandler) get(w http.ResponseWriter, r *http.Request) {
	payment, err := h.service.GetScheduledPayment(r.Context(), h.actor(r), chi.URLParam(r, "paymentID"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "scheduled payment not found")
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

// create schedules a new recurring payment.
//
//	@Summary	Create scheduled payment
//	@Tags		scheduled-payments
//	@Accept		json
//	@Produce	json
//	@Param		request	body		scheduledPaymentRequest	true	"Scheduled payment"
//	@Success	201		{object}	domain.ScheduledPayment
//	@Failure	400		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Router		/billpiggy/api/v1/scheduled-payments/ [post]
func (h scheduledPaymentHandler) create(w http.ResponseWriter, r *http.Request) {
	var request scheduledPaymentRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	payment, err := h.service.CreateScheduledPayment(r.Context(), h.actor(r), h.payment(request))
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid scheduled payment")
		return
	}
	writeJSON(w, http.StatusCreated, payment)
}

// update replaces an existing scheduled payment owned by the current user.
//
//	@Summary	Update scheduled payment
//	@Tags		scheduled-payments
//	@Accept		json
//	@Produce	json
//	@Param		paymentID	path		string					true	"Scheduled payment ID"
//	@Param		request		body		scheduledPaymentRequest	true	"Scheduled payment"
//	@Success	200			{object}	domain.ScheduledPayment
//	@Failure	400			{object}	map[string]string
//	@Failure	403			{object}	map[string]string
//	@Failure	404			{object}	map[string]string
//	@Router		/billpiggy/api/v1/scheduled-payments/{paymentID} [put]
func (h scheduledPaymentHandler) update(w http.ResponseWriter, r *http.Request) {
	var request scheduledPaymentRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	payment, err := h.service.UpdateScheduledPayment(r.Context(), h.actor(r), chi.URLParam(r, "paymentID"), h.payment(request))
	if errors.Is(err, service.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "scheduled payment not found")
		return
	}
	if errors.Is(err, service.ErrForbidden) {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid scheduled payment")
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

// delete soft-deletes a scheduled payment, stopping future occurrences.
// Expenses it already posted are left untouched.
//
//	@Summary	Delete scheduled payment
//	@Tags		scheduled-payments
//	@Param		paymentID	path	string	true	"Scheduled payment ID"
//	@Success	204
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/scheduled-payments/{paymentID} [delete]
func (h scheduledPaymentHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteScheduledPayment(r.Context(), h.actor(r), chi.URLParam(r, "paymentID")); err != nil {
		writeJSONError(w, http.StatusNotFound, "scheduled payment not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
