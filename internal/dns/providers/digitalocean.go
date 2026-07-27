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
	dns.Register("digitalocean", func(cfg dns.Config) (dns.Provider, error) {
		if cfg.Token == "" {
			return nil, fmt.Errorf("digitalocean: token required")
		}
		baseURL := cfg.APIURL
		if baseURL == "" {
			baseURL = "https://api.digitalocean.com"
		}
		return &doProvider{
			token:   cfg.Token,
			baseURL: baseURL,
			client:  dns.DefaultHTTPClient(),
		}, nil
	})
}

type doProvider struct {
	token   string
	baseURL string
	client  *http.Client
}

type doRecord struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
	TTL  int    `json:"ttl"`
}

func (p *doProvider) Name() string { return "digitalocean" }

func (p *doProvider) EnsureRecord(ctx context.Context, zone, name, recordType, value string, ttl int) (string, bool, error) {
	// Check existing
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/v2/domains/"+zone+"/records", nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("digitalocean: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		DomainRecords []doRecord `json:"domain_records"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	for _, r := range lr.DomainRecords {
		if r.Name == name && r.Type == recordType {
			if r.Data == value {
				return fmt.Sprintf("%d", r.ID), true, nil // exists
			}
			// Wrong value — delete, will re-create
			delReq, _ := http.NewRequestWithContext(ctx, "DELETE", p.baseURL+"/v2/domains/"+zone+"/records/"+fmt.Sprintf("%d", r.ID), nil)
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
	creq, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v2/domains/"+zone+"/records", bytes.NewReader(b))
	creq.Header.Set("Authorization", "Bearer "+p.token)
	creq.Header.Set("Content-Type", "application/json")

	cresp, err := p.client.Do(creq)
	if err != nil {
		return "", false, fmt.Errorf("digitalocean: create record: %w", err)
	}
	defer cresp.Body.Close()

	var cr struct {
		DomainRecord doRecord `json:"domain_record"`
	}
	json.NewDecoder(cresp.Body).Decode(&cr)
	return fmt.Sprintf("%d", cr.DomainRecord.ID), false, nil
}

func (p *doProvider) DeleteRecord(ctx context.Context, zone, recordID string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", p.baseURL+"/v2/domains/"+zone+"/records/"+recordID, nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("digitalocean: delete record: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *doProvider) ListRecords(ctx context.Context, zone string) ([]dns.Record, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/v2/domains/"+zone+"/records", nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("digitalocean: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		DomainRecords []doRecord `json:"domain_records"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	result := make([]dns.Record, 0, len(lr.DomainRecords))
	for _, r := range lr.DomainRecords {
		result = append(result, dns.Record{
			ID:    fmt.Sprintf("%d", r.ID),
			Name:  r.Name,
			Type:  r.Type,
			Value: r.Data,
			TTL:   r.TTL,
		})
	}
	return result, nil
}
