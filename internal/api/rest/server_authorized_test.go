package rest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codetrek/syntrix/internal/authz"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAuthorized_NoAuthzPassThrough(t *testing.T) {
	s := &Handler{}
	handler := s.authorized(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}, "read")

	req := httptest.NewRequest("GET", "/api/v1/foo", nil)
	req.SetPathValue("path", "col/doc")
	w := httptest.NewRecorder()

	handler(w, req)

	assert.Equal(t, http.StatusTeapot, w.Code)
}

func TestAuthorized_EvaluateError(t *testing.T) {
	engine := new(MockQueryService)
	authzSvc := new(MockAuthzService)
	s := &Handler{engine: engine, authz: authzSvc}

	engine.On("GetDocument", mock.Anything, "col/doc").Return(nil, model.ErrNotFound)
	authzSvc.On("Evaluate", mock.Anything, "col/doc", "read", mock.Anything, (*authz.Resource)(nil)).Return(false, errors.New("eval error"))

	req := httptest.NewRequest("GET", "/api/v1/foo", nil)
	req.SetPathValue("path", "col/doc")
	ctx := context.WithValue(req.Context(), "userID", "user-1")
	ctx = context.WithValue(ctx, "roles", []string{"admin", "user"})
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	s.authorized(func(http.ResponseWriter, *http.Request) {}, "read")(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	engine.AssertExpectations(t)
	authzSvc.AssertExpectations(t)
}

func TestAuthorized_Denied(t *testing.T) {
	engine := new(MockQueryService)
	authzSvc := new(MockAuthzService)
	s := &Handler{engine: engine, authz: authzSvc}

	engine.On("GetDocument", mock.Anything, "col/doc").Return(nil, model.ErrNotFound)
	authzSvc.On("Evaluate", mock.Anything, "col/doc", "read", mock.Anything, (*authz.Resource)(nil)).Return(false, nil)

	req := httptest.NewRequest("GET", "/api/v1/foo", nil)
	req.SetPathValue("path", "col/doc")
	w := httptest.NewRecorder()

	s.authorized(func(http.ResponseWriter, *http.Request) {}, "read")(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	engine.AssertExpectations(t)
	authzSvc.AssertExpectations(t)
}

func TestAuthorized_AllowedWithExistingAndNewData(t *testing.T) {
	engine := new(MockQueryService)
	authzSvc := new(MockAuthzService)
	s := &Handler{engine: engine, authz: authzSvc}

	existing := model.Document{"id": "123", "field": "old", "version": 1, "collection": "c"}
	engine.On("GetDocument", mock.Anything, "col/doc").Return(existing, nil)

	authzSvc.On("Evaluate", mock.Anything, "col/doc", "update", mock.MatchedBy(func(req authz.Request) bool {
		if req.Auth.UID != "user-1" {
			return false
		}
		roles, ok := req.Auth.Token["roles"].([]interface{})
		if !ok || len(roles) != 1 || roles[0] != "admin" {
			return false
		}
		if req.Resource == nil || req.Resource.Data["field"] != "new" {
			return false
		}
		return true
	}), mock.MatchedBy(func(res *authz.Resource) bool {
		return res != nil && res.ID == "123" && res.Data["field"] == "old" && res.Data["version"] == nil && res.Data["collection"] == nil
	})).Return(true, nil)

	req := httptest.NewRequest("PUT", "/api/v1/col/doc", strings.NewReader(`{"field":"new"}`))
	req.SetPathValue("path", "col/doc")
	ctx := context.WithValue(req.Context(), "userID", "user-1")
	ctx = context.WithValue(ctx, "roles", []string{"admin"})
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	called := false
	s.authorized(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			called = true
		}
		w.WriteHeader(http.StatusOK)
	}, "update")(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	engine.AssertExpectations(t)
	authzSvc.AssertExpectations(t)
}
