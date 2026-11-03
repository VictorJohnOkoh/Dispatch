package hub

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
)

// Direct handler tests supply the address net/http sets on a real connection.
func clientRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	if r.Host == "example.com" {
		r.Host = "127.0.0.1:7700"
	}
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7700}
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
}
