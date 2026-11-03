package hub

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

func checkClient(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !listeningHost(r) {
			refuseClient(w, "open the Client using the Hub's loopback address and port")
			return
		}
		// Non-browser callers can omit Origin. Host is checked for every request.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			origins := r.Header.Values("Origin")
			if len(origins) > 1 || (len(origins) == 1 && !sameOrigin(origins[0], r)) {
				refuseClient(w, "commands must come from this Client's own page")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func listeningHost(r *http.Request) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	_, port, err := net.SplitHostPort(local.String())
	if err != nil {
		return false
	}
	host, requestedPort, err := net.SplitHostPort(r.Host)
	if err != nil || requestedPort != port {
		return false
	}
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func sameOrigin(origin string, r *http.Request) bool {
	at, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && at.Scheme == scheme && strings.EqualFold(at.Host, r.Host) &&
		at.User == nil && at.Path == "" && at.RawQuery == "" && !at.ForceQuery && at.Fragment == "" && !strings.Contains(origin, "#")
}

func refuseClient(w http.ResponseWriter, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(protocol.Refusal{Reason: protocol.ReasonForbidden, Detail: detail})
}
