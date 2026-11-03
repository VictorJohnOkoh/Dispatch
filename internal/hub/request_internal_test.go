package hub

import (
	"io"
	"net/http"
	"net/http/httptest"
)

// Direct handler tests name the Hub by its loopback address.
func clientRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	if r.Host == "example.com" {
		r.Host = "127.0.0.1:7700"
	}
	return r
}
