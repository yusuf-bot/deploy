package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"deploy/internal/dns"
)

func init() {
	dns.Register("vultr", func(cfg dns.Config) (dns.Provider, error) {
		if cfg.Token == "" {
			return nil, fmt.Errorf("vultr: token required")
		}
		baseURL := cfg.APIURL
		if baseURL == "" {
			baseURL = "https://api.vultr.com/v2"
		}
		return &vultrProvider{
			token:   cfg.Token,
			baseURL: baseURL,
			client:  dns.DefaultHTTPClient(),
		}, nil
	})
}

type vultrProvider struct {
	token   string
	baseURL string
	client  *http.Client
}

type vultrRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Data     string `json:"data"`
	Priority int    `json:"priority"`
	TTL      int    `json:"ttl"`
}

func (p *vultrProvider) Name() string { return "vultr" }

func (p *vultrProvider) EnsureRecord(ctx context.Context, zone, name, recordType, value string, ttl int) (string, bool, error) {
	// Check existing records
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/domains/"+zone+"/records", nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("vultr: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		Records []vultrRecord `json:"records"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	for _, r := range lr.Records {
		if r.Name == name && r.Type == recordType {
			if r.Data == value {
				return r.ID, true, nil
			}
			// Wrong value — delete
			delReq, _ := http.NewRequestWithContext(ctx, "DELETE", p.baseURL+"/domains/"+zone+"/records/"+r.ID, nil)
			delReq.Header.Set("Authorization", "Bearer "+p.token)
			p.client.Do(delReq)
		}
	}

	// Create
	body := map[string]interface{}{
		"type": recordType,
		"name": name,
		"data": value,
	}
	if ttl > 0 {
		body["ttl"] = ttl
	}
	b, _ := json.Marshal(body)
	creq, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/domains/"+zone+"/records", bytes.NewReader(b))
	creq.Header.Set("Authorization", "Bearer "+p.token)
	creq.Header.Set("Content-Type", "application/json")

	cresp, err := p.client.Do(creq)
	if err != nil {
		return "", false, fmt.Errorf("vultr: create record: %w", err)
	}
	defer cresp.Body.Close()

	var cr struct {
		Record vultrRecord `json:"record"`
	}
	json.NewDecoder(cresp.Body).Decode(&cr)
	return cr.Record.ID, false, nil
}

func (p *vultrProvider) DeleteRecord(ctx context.Context, zone, recordID string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", p.baseURL+"/domains/"+zone+"/records/"+recordID, nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("vultr: delete record: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *vultrProvider) ListRecords(ctx context.Context, zone string) ([]dns.Record, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/domains/"+zone+"/records", nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vultr: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		Records []vultrRecord `json:"records"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	result := make([]dns.Record, 0, len(lr.Records))
	for _, r := range lr.Records {
		result = append(result, dns.Record{
			ID:    r.ID,
			Name:  r.Name,
			Type:  r.Type,
			Value: r.Data,
			TTL:   r.TTL,
		})
	}
	return result, nil
}
