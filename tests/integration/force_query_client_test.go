package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalQueryServer simulates the query service endpoints used by API when force query client is enabled.
type minimalQueryServer struct {
	server     *httptest.Server
	hitsCreate int64
	hitsGet    int64
}

func newMinimalQueryServer(t *testing.T) *minimalQueryServer {
	h := &minimalQueryServer{}

	mux := http.NewServeMux()

	mux.HandleFunc("/internal/v1/document/create", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&h.hitsCreate, 1)
		var doc map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("/internal/v1/document/get", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&h.hitsGet, 1)
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)

		// respond with a dummy document using provided path
		path := req["path"]
		doc := map[string]interface{}{
			"id":         path,
			"collection": "forced",
			"msg":        "from-query-client",
			"version":    1,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})

	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

func TestIntegration_ForceQueryClient_API(t *testing.T) {
	t.Parallel()
	queryStub := newMinimalQueryServer(t)

	cfgMod := func(cfg *config.Config) {
		cfg.Gateway.QueryServiceURL = queryStub.server.URL
		cfg.Identity.AuthZ.RulesFile = "" // disable auth enforcement
		cfg.Identity.AuthN.PrivateKeyFile = filepath.Join(t.TempDir(), "auth_private.pem")
	}

	optsMod := func(opts *services.Options) {
		opts.RunQuery = false
		opts.RunCSP = false
		opts.RunTriggerEvaluator = false
		opts.RunTriggerWorker = false
		opts.ForceQueryClient = true
	}

	env := setupServiceEnvWithOptions(t, "", []func(*config.Config){cfgMod}, []func(*services.Options){optsMod})
	defer env.Cancel()

	token := env.GetToken(t, "force-user", "user")

	// Perform create through API; should hit stubbed query service via query.NewClient
	body := map[string]interface{}{"msg": "hello"}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, env.APIURL+"/api/v1/forced", bytes.NewBuffer(b))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Verify stub received calls
	assert.Greater(t, atomic.LoadInt64(&queryStub.hitsCreate), int64(0))
	assert.Greater(t, atomic.LoadInt64(&queryStub.hitsGet), int64(0))
}
