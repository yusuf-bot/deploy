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
	dns.Register("hetzner", func(cfg dns.Config) (dns.Provider, error) {
		if cfg.Token == "" {
			return nil, fmt.Errorf("hetzner: token required")
		}
		baseURL := cfg.APIURL
		if baseURL == "" {
			baseURL = "https://api.hetzner.cloud/v1"
		}
		return &hetznerProvider{
			token:   cfg.Token,
			baseURL: baseURL,
			client:  dns.DefaultHTTPClient(),
		}, nil
	})
}

type hetznerProvider struct {
	token   string
	baseURL string
	client  *http.Client
}

type hzZone struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type hzRRSet struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Type    string      `json:"type"`
	TTL     int         `json:"ttl"`
	Records []hzRRValue `json:"records"`
}

type hzRRValue struct {
	Value string `json:"value"`
}

func (p *hetznerProvider) Name() string { return "hetzner" }

func (p *hetznerProvider) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequestWithContext(ctx, method, p.baseURL+path, &buf)
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	return p.client.Do(req)
}

func (p *hetznerProvider) findZone(ctx context.Context, domain string) (int, error) {
	resp, err := p.do(ctx, "GET", "/zones?name="+domain, nil)
	if err != nil {
		return 0, fmt.Errorf("hetzner: list zones: %w", err)
	}
	defer resp.Body.Close()

	var zr struct {
		Zones []hzZone `json:"zones"`
	}
	json.NewDecoder(resp.Body).Decode(&zr)

	for _, z := range zr.Zones {
		if z.Name == domain {
			return z.ID, nil
		}
	}
	return 0, fmt.Errorf("hetzner: zone %q not found", domain)
}

func (p *hetznerProvider) EnsureRecord(ctx context.Context, zone, name, recordType, value string, ttl int) (string, bool, error) {
	zoneID, err := p.findZone(ctx, zone)
	if err != nil {
		return "", false, err
	}

	// Check if RRSet already exists and matches
	resp, err := p.do(ctx, "GET", fmt.Sprintf("/zones/%d/rrsets/%s/%s", zoneID, name, recordType), nil)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var rr struct {
				RRSet hzRRSet `json:"rrset"`
			}
			json.NewDecoder(resp.Body).Decode(&rr)
			for _, r := range rr.RRSet.Records {
				if r.Value == value {
					existingID := fmt.Sprintf("%s/%s", name, recordType)
					return existingID, true, nil
				}
			}
		}
	}

	// Create
	createBody := struct {
		Name    string      `json:"name"`
		Type    string      `json:"type"`
		TTL     int         `json:"ttl"`
		Records []hzRRValue `json:"records"`
	}{
		Name:    name,
		Type:    recordType,
		TTL:     ttl,
		Records: []hzRRValue{{Value: value}},
	}
	if ttl <= 0 {
		createBody.TTL = 3600
	}

	cresp, err := p.do(ctx, "POST", fmt.Sprintf("/zones/%d/rrsets", zoneID), createBody)
	if err != nil {
		return "", false, fmt.Errorf("hetzner: create/update rrset: %w", err)
	}
	defer cresp.Body.Close()

	// 409 = already exists, use set_records action instead
	if cresp.StatusCode == 409 {
		setBody := struct {
			Records []hzRRValue `json:"records"`
		}{Records: []hzRRValue{{Value: value}}}

		sresp, err := p.do(ctx, "POST", fmt.Sprintf("/zones/%d/rrsets/%s/%s/actions/set_records", zoneID, name, recordType), setBody)
		if err != nil {
			return "", false, fmt.Errorf("hetzner: update records: %w", err)
		}
		sresp.Body.Close()
	} else if cresp.StatusCode >= 400 {
		return "", false, fmt.Errorf("hetzner: create rrset failed (status %d)", cresp.StatusCode)
	}

	return fmt.Sprintf("%s/%s", name, recordType), false, nil
}

func (p *hetznerProvider) DeleteRecord(ctx context.Context, zone, recordID string) error {
	zoneID, err := p.findZone(ctx, zone)
	if err != nil {
		return err
	}
	resp, err := p.do(ctx, "DELETE", fmt.Sprintf("/zones/%d/rrsets/%s", zoneID, recordID), nil)
	if err != nil {
		return fmt.Errorf("hetzner: delete rrset: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *hetznerProvider) ListRecords(ctx context.Context, zone string) ([]dns.Record, error) {
	zoneID, err := p.findZone(ctx, zone)
	if err != nil {
		return nil, err
	}

	resp, err := p.do(ctx, "GET", fmt.Sprintf("/zones/%d/rrsets?per_page=100", zoneID), nil)
	if err != nil {
		return nil, fmt.Errorf("hetzner: list rrsets: %w", err)
	}
	defer resp.Body.Close()

	var lr struct {
		RRSets []hzRRSet `json:"rrsets"`
	}
	json.NewDecoder(resp.Body).Decode(&lr)

	result := make([]dns.Record, 0, len(lr.RRSets))
	for _, rr := range lr.RRSets {
		for _, r := range rr.Records {
			result = append(result, dns.Record{
				ID:    rr.ID,
				Name:  rr.Name,
				Type:  rr.Type,
				Value: r.Value,
				TTL:   rr.TTL,
			})
		}
	}
	return result, nil
}
