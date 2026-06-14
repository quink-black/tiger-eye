package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/quink/tiger-eye/internal/config"
	"github.com/quink/tiger-eye/internal/event"
)

// eventsResponse mirrors the node's /events payload.
type eventsResponse struct {
	Events  []event.Event `json:"events"`
	LastSeq uint64        `json:"last_seq"`
}

// puller fetches incremental events from one host. The collector creates one
// puller per host. For ssh-mode hosts the baseURL points at a local port that a
// persistent `ssh -L` tunnel forwards to the remote node's loopback port.
type puller struct {
	host    config.Host
	baseURL string
	client  *http.Client
}

func newPuller(h config.Host, baseURL string) *puller {
	return &puller{
		host:    h,
		baseURL: baseURL,
		// Timeout must exceed the node's max long-poll (25s) plus slack.
		client: &http.Client{Timeout: 35 * time.Second},
	}
}

// poll performs one long-poll for events with Seq > since, returning the events
// and the new high-water seq.
func (p *puller) poll(ctx context.Context, since uint64) ([]event.Event, uint64, error) {
	url := fmt.Sprintf("%s/events?since=%d&wait=25000", p.baseURL, since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, since, err
	}
	if p.host.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.host.Token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, since, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, since, fmt.Errorf("node %s: %s: %s", p.host.Name, resp.Status, body)
	}
	var er eventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, since, err
	}
	last := since
	if er.LastSeq > last {
		last = er.LastSeq
	}
	return er.Events, last, nil
}
