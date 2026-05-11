package mcpclient

import (
	"net/http"
	"strings"
)

// httpClientWithHeaders returns a shallow copy of base whose transport injects
// the given headers on every request. If headers is empty, base is returned as-is
// (or [http.DefaultClient] when base is nil).
func httpClientWithHeaders(base *http.Client, headers map[string]string) *http.Client {
	if len(headers) == 0 {
		if base == nil {
			return http.DefaultClient
		}
		return base
	}
	if base == nil {
		base = http.DefaultClient
	}
	inner := base.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	clone := *base
	clone.Transport = &headerInjectingTransport{
		base:    inner,
		headers: headers,
	}
	return &clone
}

type headerInjectingTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerInjectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}
