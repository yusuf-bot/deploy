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
	dns.Register("cloudflare", func(cfg dns.Config) (dns.Provider, error) {
		if cfg.Token == "" {
			return nil, fmt.Errorf("cloudflare: token required")
		}
		baseURL := cfg.APIURL
		if baseURL == "" {
			baseURL = "https://api.cloudflare.com/client/v4"
		}
		return &cloudflareProvider{
			token:   cfg.Token,
			baseURL: baseURL,
			client:  dns.DefaultHTTPClient(),
		}, nil
	})
}

type cloudflareProvider struct {
	token   string
	baseURL string
	client  *http.Client
}

type cfResponse struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

func (p *cloudflareProvider) Name() string { return "cloudflare" }

func (p *cloudflareProvider) findZone(ctx context.Context, domain string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/zones?name="+domain, nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloudflare: list zones: %w", err)
	}
	defer resp.Body.Close()

	var r cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("cloudflare: decode zones: %w", err)
	}
	if !r.Success {
		return "", fmt.Errorf("cloudflare: list zones failed")
	}

	var zones []cfZone
	if err := json.Unmarshal(r.Result, &zones); err != nil {
		return "", fmt.Errorf("cloudflare: parse zones: %w", err)
	}
	for _, z := range zones {
		if z.Name == domain {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("cloudflare: zone %q not found", domain)
}

func (p *cloudflareProvider) EnsureRecord(ctx context.Context, zone, name, recordType, value string, ttl int) (string, bool, error) {
	zoneID, err := p.findZone(ctx, zone)
	if err != nil {
		return "", false, err
	}

	// Check if record already exists
	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/zones/"+zoneID+"/dns_records?type="+recordType+"&name="+name, nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("cloudflare: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr cfResponse
	json.NewDecoder(resp.Body).Decode(&lr)

	var records []cfRecord
	if lr.Success {
		json.Unmarshal(lr.Result, &records)
		for _, r := range records {
			if r.Name == name && r.Type == recordType && r.Content == value {
				return r.ID, true, nil // already exists with same value
			}
			if r.Name == name && r.Type == recordType && r.Content != value {
				// Exists but wrong value — delete it first, we'll re-create
				p.deleteRecordByID(ctx, zoneID, r.ID)
			}
		}
	}

	// Create the record
	body := map[string]interface{}{
		"type":    recordType,
		"name":    name,
		"content": value,
		"ttl":     ttl,
	}
	if ttl <= 0 {
		body["ttl"] = 1 // auto
	}

	b, _ := json.Marshal(body)
	creq, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/zones/"+zoneID+"/dns_records", bytes.NewReader(b))
	creq.Header.Set("Authorization", "Bearer "+p.token)
	creq.Header.Set("Content-Type", "application/json")

	cresp, err := p.client.Do(creq)
	if err != nil {
		return "", false, fmt.Errorf("cloudflare: create record: %w", err)
	}
	defer cresp.Body.Close()

	var cr cfResponse
	json.NewDecoder(cresp.Body).Decode(&cr)
	if !cr.Success {
		return "", false, fmt.Errorf("cloudflare: create record failed: %s", string(cr.Result))
	}

	var record cfRecord
	json.Unmarshal(cr.Result, &record)
	return record.ID, false, nil
}

func (p *cloudflareProvider) deleteRecordByID(ctx context.Context, zoneID, recordID string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", p.baseURL+"/zones/"+zoneID+"/dns_records/"+recordID, nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: delete record: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *cloudflareProvider) DeleteRecord(ctx context.Context, zone, recordID string) error {
	zoneID, err := p.findZone(ctx, zone)
	if err != nil {
		return err
	}
	return p.deleteRecordByID(ctx, zoneID, recordID)
}

func (p *cloudflareProvider) ListRecords(ctx context.Context, zone string) ([]dns.Record, error) {
	zoneID, err := p.findZone(ctx, zone)
	if err != nil {
		return nil, err
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/zones/"+zoneID+"/dns_records?per_page=100", nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: list records: %w", err)
	}
	defer resp.Body.Close()

	var lr cfResponse
	json.NewDecoder(resp.Body).Decode(&lr)

	var records []cfRecord
	if err := json.Unmarshal(lr.Result, &records); err != nil {
		return nil, fmt.Errorf("cloudflare: parse records: %w", err)
	}

	result := make([]dns.Record, 0, len(records))
	for _, r := range records {
		result = append(result, dns.Record{
			ID:    r.ID,
			Name:  r.Name,
			Type:  r.Type,
			Value: r.Content,
			TTL:   r.TTL,
		})
	}
	return result, nil
}
