package rest

import (
	"encoding/json"
	"net/http"

	"github.com/codetrek/syntrix/pkg/model"
)

func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	var q model.Query
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := validateQuery(q); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	docs, err := h.engine.ExecuteQuery(r.Context(), q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(docs)
}
