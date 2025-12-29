package rest

import (
	"encoding/json"
	"net/http"

	"github.com/codetrek/syntrix/pkg/model"
)

func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	var q model.Query
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid request body")
		return
	}

	if err := validateQuery(q); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "Invalid query parameters")
		return
	}

	tenant, ok := h.tenantOrError(w, r)
	if !ok {
		return
	}

	docs, err := h.engine.ExecuteQuery(r.Context(), tenant, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to execute query")
		return
	}

	writeJSON(w, http.StatusOK, docs)
}
