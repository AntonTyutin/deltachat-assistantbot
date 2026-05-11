package mcpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientWithHeaders(t *testing.T) {
	t.Parallel()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Test")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := httpClientWithHeaders(srv.Client(), map[string]string{"X-Test": "injected"})
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if seen != "injected" {
		t.Fatalf("header: got %q", seen)
	}
}
