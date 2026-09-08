package runtime

import (
	"context"
	"net/http"
	"time"
)

// httpClient is used only for workload health checks on loopback.
// Timeout is per-request; the poll loop in HealthGET is the budget.
var httpClient = &http.Client{
	Timeout: 2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func newGET(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}
