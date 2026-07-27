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
	dns.Register("linode", func(cfg dns.Config) (dns.Provider, error) {
		if cfg.Token == "" {
			return nil, fmt.Errorf("linode: token required")
		}
		baseURL := cfg.APIURL
		if baseURL == "" {
			baseURL = "https://api.linode.com/v4"
		}
		return &linodeProvider{
			token:   cfg.Token,
			baseURL: baseURL,
			client:  dns.DefaultHTTPClient(),
		}, nil
	})
}

type linodeProvider struct {
	token   string
	baseURL string
	client  *http.Client
}

type linodeDomain struct {
	ID     int    `json:"id"`
	Domain string `json:"domain"`
}

type linodeRecord struct {
	ID     int    `json:"id"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Target string `json:"target"`
	TTLSec int    `json:"ttl_sec"`
}

func (p *linodeProvider) Name() string { return "linode" }

func (p *linodeProvider) findDomainID(ctx context.Context, domain string) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/domains", nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("linode: list domains: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		Data []linodeDomain `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	for _, d := range lr.Data {
		if d.Domain == domain {
			return d.ID, nil
		}
	}
	return 0, fmt.Errorf("linode: domain %q not found", domain)
}

func (p *linodeProvider) EnsureRecord(ctx context.Context, zone, name, recordType, value string, ttl int) (string, bool, error) {
	domainID, err := p.findDomainID(ctx, zone)
	if err != nil {
		return "", false, err
	}

	// List existing
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+fmt.Sprintf("/domains/%d/records", domainID), nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("linode: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		Data []linodeRecord `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	for _, r := range lr.Data {
		if r.Name == name && r.Type == recordType {
			if r.Target == value {
				return fmt.Sprintf("%d", r.ID), true, nil
			}
			// Wrong value — delete
			delReq, _ := http.NewRequestWithContext(ctx, "DELETE", p.baseURL+fmt.Sprintf("/domains/%d/records/%d", domainID, r.ID), nil)
			delReq.Header.Set("Authorization", "Bearer "+p.token)
			p.client.Do(delReq)
		}
	}

	// Create
	body := map[string]interface{}{
		"type":   recordType,
		"name":   name,
		"target": value,
	}
	if ttl > 0 {
		body["ttl_sec"] = ttl
	}
	b, _ := json.Marshal(body)
	creq, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+fmt.Sprintf("/domains/%d/records", domainID), bytes.NewReader(b))
	creq.Header.Set("Authorization", "Bearer "+p.token)
	creq.Header.Set("Content-Type", "application/json")

	cresp, err := p.client.Do(creq)
	if err != nil {
		return "", false, fmt.Errorf("linode: create record: %w", err)
	}
	defer cresp.Body.Close()

	var cr struct {
		Data linodeRecord `json:"data"`
	}
	json.NewDecoder(cresp.Body).Decode(&cr)
	return fmt.Sprintf("%d", cr.Data.ID), false, nil
}

func (p *linodeProvider) DeleteRecord(ctx context.Context, zone, recordID string) error {
	domainID, err := p.findDomainID(ctx, zone)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "DELETE", p.baseURL+fmt.Sprintf("/domains/%d/records/%s", domainID, recordID), nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("linode: delete record: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *linodeProvider) ListRecords(ctx context.Context, zone string) ([]dns.Record, error) {
	domainID, err := p.findDomainID(ctx, zone)
	if err != nil {
		return nil, err
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+fmt.Sprintf("/domains/%d/records", domainID), nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("linode: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		Data []linodeRecord `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	result := make([]dns.Record, 0, len(lr.Data))
	for _, r := range lr.Data {
		result = append(result, dns.Record{
			ID:    fmt.Sprintf("%d", r.ID),
			Name:  r.Name,
			Type:  r.Type,
			Value: r.Target,
			TTL:   r.TTLSec,
		})
	}
	return result, nil
}
