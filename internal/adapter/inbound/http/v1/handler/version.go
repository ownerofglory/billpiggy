package handler

import "net/http"

// GetVersionPath is the URL path for retrieving the application version.
const GetVersionPath = basePathV1 + "/version"

// BillPiggyVersion holds the current version of the application.
//
// It is initialized to "default" and should typically be set at build time
// using ldflags, for example:
//
//	go build -ldflags "-X 'package/path.BillPiggyVersion=v1.2.3'"
var BillPiggyVersion = "default"

// HandleGetVersion is an HTTP handler that responds with the current
// application version.
//
//	@Summary	Get application version
//	@Tags		health
//	@Produce	plain
//	@Success	200	{string}	string
//	@Router		/billpiggy/api/v1/version [get]
func HandleGetVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(BillPiggyVersion))
}
