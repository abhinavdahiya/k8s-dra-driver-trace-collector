package alloy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Reload_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/-/reload", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	err := client.Reload(context.Background())
	require.NoError(t, err)
}

func TestClient_Reload_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid config: syntax error in claim-abc.alloy"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	err := client.Reload(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reload failed (HTTP 400)")
	assert.Contains(t, err.Error(), "syntax error in claim-abc.alloy")
}

func TestClient_Reload_ConnectionError(t *testing.T) {
	client := NewClient("http://127.0.0.1:0")
	err := client.Reload(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reload request failed")
}

func TestClient_Healthy_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/-/healthy", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	err := client.Healthy(context.Background())
	require.NoError(t, err)
}

func TestClient_Healthy_Unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("unhealthy components: otelcol.receiver.otlp.abc"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	err := client.Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alloy unhealthy (HTTP 500)")
	assert.Contains(t, err.Error(), "unhealthy components")
}

func TestClient_Healthy_ConnectionError(t *testing.T) {
	client := NewClient("http://127.0.0.1:0")
	err := client.Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "healthy request failed")
}

func TestClient_Reload_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := NewClient(srv.URL)
	err := client.Reload(ctx)
	require.Error(t, err)
}
