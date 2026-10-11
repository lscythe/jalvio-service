// Package stopapi reads halte records from the upstream stop API. Only the
// daily facilities sync uses it; user requests never reach the upstream.
package stopapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lscythe/jalvio-service/internal/geo"
)

const (
	// maxResponseSize bounds memory use; a one-letter name search is about 2.5 MB.
	maxResponseSize = 16 << 20
	renewBefore     = time.Minute
	// searchLimit is above the total stop count, since name search is not
	// limited by distance.
	searchLimit = "10000"
)

type Config struct {
	// BaseURL includes the API version path, without a trailing slash.
	BaseURL    string
	AppVersion string
	DeviceID   string
	UserAgent  string
}

type Stop struct {
	ID           string
	Name         string
	PlatformCode string
	Description  string
	Facilities   []Facility
}

// Facility is an upstream facility name and its display title. Title is
// empty when the record lists only the name.
type Facility struct {
	Name  string
	Title string
}

// Client shares one guest session between calls, logging in on first use
// and again shortly before the token expires or after it is rejected.
type Client struct {
	http *http.Client
	cfg  Config

	mu      sync.Mutex
	token   string
	expires time.Time
}

func New(httpClient *http.Client, cfg Config) *Client {
	return &Client{http: httpClient, cfg: cfg}
}

// SearchStops returns every stop whose name contains query, sorted by
// distance from near.
func (c *Client) SearchStops(ctx context.Context, query string, near geo.Point) ([]Stop, error) {
	q := url.Values{
		"search_by": {query},
		"latitude":  {strconv.FormatFloat(near.Lat, 'f', -1, 64)},
		"longitude": {strconv.FormatFloat(near.Lon, 'f', -1, 64)},
		"limit":     {searchLimit},
	}
	var env struct {
		Data []stopRecord `json:"data"`
	}
	if err := c.get(ctx, "/bus-stop?"+q.Encode(), &env); err != nil {
		return nil, err
	}
	stops := make([]Stop, 0, len(env.Data))
	for _, r := range env.Data {
		stops = append(stops, r.stop())
	}
	return stops, nil
}

// Stop returns the record for id. For a halte platform the upstream answers
// with its parent station.
func (c *Client) Stop(ctx context.Context, id string) (Stop, error) {
	var env struct {
		Data stopRecord `json:"data"`
	}
	if err := c.get(ctx, "/bus-stop/"+url.PathEscape(id), &env); err != nil {
		return Stop{}, err
	}
	return env.Data.stop(), nil
}

type stopRecord struct {
	ID           string   `json:"stop_id"`
	Name         string   `json:"stop_name"`
	PlatformCode string   `json:"platform_code"`
	Description  string   `json:"description"`
	Facilities   []string `json:"facilities"`
	V2Facilities []struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	} `json:"v2_facilities"`
}

func (r stopRecord) stop() Stop {
	s := Stop{ID: r.ID, Name: r.Name, PlatformCode: r.PlatformCode, Description: r.Description}
	seen := map[string]bool{}
	for _, f := range r.V2Facilities {
		s.Facilities = append(s.Facilities, Facility{Name: f.Name, Title: f.Title})
		seen[f.Name] = true
	}
	for _, name := range r.Facilities {
		if !seen[name] {
			s.Facilities = append(s.Facilities, Facility{Name: name})
		}
	}
	return s
}

type statusError struct{ status string }

func (e *statusError) Error() string { return "unexpected status " + e.status }

func (c *Client) get(ctx context.Context, path string, out any) error {
	token, err := c.session(ctx)
	if err != nil {
		return err
	}
	err = c.do(ctx, http.MethodGet, path, token, out)
	if se := (*statusError)(nil); errors.As(err, &se) && strings.HasPrefix(se.status, "401") {
		c.invalidate(token)
		if token, err = c.session(ctx); err != nil {
			return err
		}
		err = c.do(ctx, http.MethodGet, path, token, out)
	}
	return err
}

func (c *Client) session(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.expires) > renewBefore {
		return c.token, nil
	}
	var resp struct {
		Token      string `json:"token"`
		ValidUntil int64  `json:"token_valid_until"`
	}
	if err := c.do(ctx, http.MethodPost, "/auth/login/guest", "", &resp); err != nil {
		return "", fmt.Errorf("guest login: %w", err)
	}
	if resp.Token == "" {
		return "", errors.New("guest login: no token in response")
	}
	c.token, c.expires = resp.Token, time.Unix(resp.ValidUntil, 0)
	return c.token, nil
}

// invalidate drops token unless another call has already replaced it.
func (c *Client) invalidate(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == token {
		c.token = ""
	}
}

func (c *Client) do(ctx context.Context, method, path, token string, out any) error {
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("X-App-Version", c.cfg.AppVersion)
	req.Header.Set("X-Device-ID", c.cfg.DeviceID)
	req.Header.Set("X-Lang", "id")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &statusError{status: resp.Status}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseSize {
		return fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
