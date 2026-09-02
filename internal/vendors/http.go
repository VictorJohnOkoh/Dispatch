package vendors

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// request does one call and hands back the status and the whole body. Every
// adapter's contact with the world goes through here, which is what makes a
// caller-supplied RoundTripper the only seam the tier-two tests need.
//
// It reads the body whatever the status is, because a Vendor's refusal is in its
// body and each adapter says something different about it. What stays with the
// adapter is the Vendor's name, its paths and the shape of its refusals.
func request(ctx context.Context, client *http.Client, method, url string, payload []byte) (int, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}
