package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// maxFeedSize bounds memory use; the official feed is about 2.5 MB.
const maxFeedSize = 64 << 20

type HTTPFetcher struct {
	client    *http.Client
	url       string
	userAgent string
	maxSize   int64
}

func NewHTTPFetcher(client *http.Client, url, userAgent string) *HTTPFetcher {
	return &HTTPFetcher{client: client, url: url, userAgent: userAgent, maxSize: maxFeedSize}
}

func (f *HTTPFetcher) Fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > f.maxSize {
		return nil, fmt.Errorf("feed exceeds %d bytes", f.maxSize)
	}
	return data, nil
}
