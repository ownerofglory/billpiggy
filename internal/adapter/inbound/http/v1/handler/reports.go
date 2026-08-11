package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
)

// RegisterReportRoutes mounts authenticated, owner-scoped report endpoints.
func RegisterReportRoutes(router chi.Router, reports inbound.ReportService, objects outbound.ObjectStore, middleware *sharedauth.Middleware) {
	h := reportHandler{service: reports, objects: objects}
	router.Route(basePathV1+"/reports", func(routes chi.Router) {
		routes.Use(middleware.RequireAuthentication)
		routes.Get("/", h.list)
		routes.Get("/{reportID}/download", h.download)
	})
}

type reportHandler struct {
	service inbound.ReportService
	objects outbound.ObjectStore
}

func (h reportHandler) owner(r *http.Request) string {
	identity, _ := sharedauth.IdentityFromContext(r.Context())
	return identity.Subject
}

// list returns the current user's generated reports, newest first.
//
//	@Summary	List generated reports
//	@Tags		reports
//	@Security	ApiKeyAuth
//	@Produce	json
//	@Success	200	{array}		domain.Report
//	@Failure	401	{object}	map[string]string
//	@Router		/billpiggy/api/v1/reports [get]
func (h reportHandler) list(w http.ResponseWriter, r *http.Request) {
	values, err := h.service.ListReports(r.Context(), h.owner(r))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not list reports")
		return
	}
	writeJSON(w, http.StatusOK, values)
}

// download redirects to an owner-scoped report's stored file.
//
//	@Summary	Download a generated report
//	@Tags		reports
//	@Security	ApiKeyAuth
//	@Param		reportID	path	string	true	"Report ID"
//	@Success	302
//	@Failure	404	{object}	map[string]string
//	@Router		/billpiggy/api/v1/reports/{reportID}/download [get]
func (h reportHandler) download(w http.ResponseWriter, r *http.Request) {
	value, err := h.service.GetReport(r.Context(), h.owner(r), chi.URLParam(r, "reportID"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "report not found")
		return
	}
	serveObject(w, r, h.objects, value.ObjectKey)
}
