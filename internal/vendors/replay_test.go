package vendors

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// recorded replays the bodies under testdata/<dir>, which were captured from a
// real Vendor on loopback. The fixtures are the specification in these tests, and
// no test in this package opens a socket.
type recorded struct {
	dir    string
	status map[string]int
	body   map[string]string // request path to a file under testdata/<dir>

	seen []*http.Request
}

func (r *recorded) RoundTrip(req *http.Request) (*http.Response, error) {
	r.seen = append(r.seen, req)

	name, ok := r.body[req.URL.Path]
	if !ok {
		return nil, errors.New("no fixture for " + req.URL.Path)
	}
	body, err := os.ReadFile(filepath.Join("testdata", r.dir, name))
	if err != nil {
		return nil, err
	}

	status := r.status[req.URL.Path]
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

// paths is the request path of every call an adapter made, in order.
func (r *recorded) paths() []string {
	out := make([]string, len(r.seen))
	for i, req := range r.seen {
		out[i] = req.URL.Path
	}
	return out
}

func sentBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read the sent body: %v", err)
	}
	return body
}
