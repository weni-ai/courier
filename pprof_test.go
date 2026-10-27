package courier

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPprofMuxServesIndexAndHeap(t *testing.T) {
	ts := httptest.NewServer(newPprofMux())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/debug/pprof/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "heap")

	resp, err = http.Get(ts.URL + "/debug/pprof/heap?debug=1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "heap profile")
}

func TestWrapStatusAuth(t *testing.T) {
	handler := wrapStatusAuth("admin", "password123", newPprofMux())
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/debug/pprof/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, `Basic realm="Authenticate"`, resp.Header.Get("WWW-Authenticate"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "Unauthorised.\n", string(body))

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/debug/pprof/", nil)
	require.NoError(t, err)
	req.SetBasicAuth("admin", "wrong")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	req, err = http.NewRequest(http.MethodGet, ts.URL+"/debug/pprof/", nil)
	require.NoError(t, err)
	req.SetBasicAuth("admin", "password123")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestWrapStatusAuthEmptyUsernameAllows(t *testing.T) {
	ts := httptest.NewServer(wrapStatusAuth("", "", newPprofMux()))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/debug/pprof/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNewConfigPprofDefaults(t *testing.T) {
	cfg := NewConfig()
	assert.True(t, cfg.EnablePprof)
	assert.Equal(t, "0.0.0.0", cfg.PprofAddress)
	assert.Equal(t, 6060, cfg.PprofPort)
}

func TestIsLoopbackAddress(t *testing.T) {
	assert.True(t, isLoopbackAddress("127.0.0.1"))
	assert.True(t, isLoopbackAddress("::1"))
	assert.True(t, isLoopbackAddress("localhost"))
	assert.True(t, isLoopbackAddress("LOCALHOST"))
	assert.False(t, isLoopbackAddress("0.0.0.0"))
	assert.False(t, isLoopbackAddress(""))
	assert.False(t, isLoopbackAddress("::"))
	assert.False(t, isLoopbackAddress("[::]"))
	assert.False(t, isLoopbackAddress("10.0.0.1"))
}

func TestPprofCanStart(t *testing.T) {
	assert.False(t, pprofCanStart(&Config{EnablePprof: false, PprofAddress: "127.0.0.1"}))
	assert.False(t, pprofCanStart(&Config{EnablePprof: true, PprofAddress: "0.0.0.0"}))
	assert.True(t, pprofCanStart(&Config{EnablePprof: true, PprofAddress: "0.0.0.0", StatusUsername: "admin"}))
	assert.True(t, pprofCanStart(&Config{EnablePprof: true, PprofAddress: "127.0.0.1"}))
}

func TestStartPprofServerDisabled(t *testing.T) {
	s := newTestPprofServerState(&Config{EnablePprof: false, PprofAddress: "127.0.0.1", PprofPort: 0})
	s.startPprofServer()
	assert.Nil(t, s.pprofServer)
}

func TestStartPprofServerRefusesNonLoopbackWithoutAuth(t *testing.T) {
	cfg := NewConfig()
	cfg.EnablePprof = true
	cfg.PprofAddress = "0.0.0.0"
	cfg.PprofPort = 6060
	cfg.StatusUsername = ""
	s := newTestPprofServerState(cfg)
	s.startPprofServer()
	assert.Nil(t, s.pprofServer)
}

func TestStartPprofServerLoopbackWithoutAuth(t *testing.T) {
	s := newTestPprofServerState(&Config{
		EnablePprof:    true,
		PprofAddress:   "127.0.0.1",
		PprofPort:      0,
		StatusUsername: "",
	})
	s.startPprofServer()
	require.NotNil(t, s.pprofServer)
	require.NoError(t, s.pprofServer.Shutdown(context.Background()))
	s.waitGroup.Wait()
}

func newTestPprofServerState(cfg *Config) *server {
	return &server{
		config:    cfg,
		waitGroup: &sync.WaitGroup{},
	}
}
