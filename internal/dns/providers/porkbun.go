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
	dns.Register("porkbun", func(cfg dns.Config) (dns.Provider, error) {
		if cfg.Token == "" || cfg.Secret == "" {
			return nil, fmt.Errorf("porkbun: api key and secret key required")
		}
		baseURL := cfg.APIURL
		if baseURL == "" {
			baseURL = "https://api.porkbun.com/api/json/v3"
		}
		return &porkbunProvider{
			apiKey:    cfg.Token,
			secretKey: cfg.Secret,
			baseURL:   baseURL,
			client:    dns.DefaultHTTPClient(),
		}, nil
	})
}

type porkbunProvider struct {
	apiKey    string
	secretKey string
	baseURL   string
	client    *http.Client
}

type porkbunRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     string `json:"ttl"`
	Prio    string `json:"prio"`
}

type porkbunResp struct {
	Status  string          `json:"status"`
	Records []porkbunRecord `json:"records,omitempty"`
	ID      string          `json:"id,omitempty"`
}

func (p *porkbunProvider) Name() string { return "porkbun" }

func (p *porkbunProvider) authBody() map[string]string {
	return map[string]string{
		"apikey":       p.apiKey,
		"secretapikey": p.secretKey,
	}
}

func (p *porkbunProvider) post(ctx context.Context, path string, body map[string]interface{}) (*http.Response, error) {
	if body == nil {
		body = make(map[string]interface{})
	}
	// Merge auth into body
	for k, v := range p.authBody() {
		body[k] = v
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return p.client.Do(req)
}

func (p *porkbunProvider) EnsureRecord(ctx context.Context, zone, name, recordType, value string, ttl int) (string, bool, error) {
	// List existing records
	resp, err := p.post(ctx, "/dns/retrieve/"+zone, nil)
	if err != nil {
		return "", false, fmt.Errorf("porkbun: retrieve records: %w", err)
	}
	defer resp.Body.Close()

	var pr porkbunResp
	json.NewDecoder(resp.Body).Decode(&pr)

	if pr.Status != "SUCCESS" {
		return "", false, fmt.Errorf("porkbun: retrieve failed")
	}

	for _, r := range pr.Records {
		if r.Name == name && r.Type == recordType {
			if r.Content == value {
				return r.ID, true, nil
			}
			// Wrong value — delete
			p.post(ctx, "/dns/delete/"+zone+"/"+r.ID, nil)
		}
	}

	// Create
	body := map[string]interface{}{
		"name":    name,
		"type":    recordType,
		"content": value,
	}
	if ttl > 0 {
		body["ttl"] = ttl
	}

	cresp, err := p.post(ctx, "/dns/create/"+zone, body)
	if err != nil {
		return "", false, fmt.Errorf("porkbun: create record: %w", err)
	}
	defer cresp.Body.Close()

	var cr porkbunResp
	json.NewDecoder(cresp.Body).Decode(&cr)
	if cr.Status != "SUCCESS" {
		return "", false, fmt.Errorf("porkbun: create failed")
	}
	return cr.ID, false, nil
}

func (p *porkbunProvider) DeleteRecord(ctx context.Context, zone, recordID string) error {
	resp, err := p.post(ctx, "/dns/delete/"+zone+"/"+recordID, nil)
	if err != nil {
		return fmt.Errorf("porkbun: delete record: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *porkbunProvider) ListRecords(ctx context.Context, zone string) ([]dns.Record, error) {
	resp, err := p.post(ctx, "/dns/retrieve/"+zone, nil)
	if err != nil {
		return nil, fmt.Errorf("porkbun: retrieve records: %w", err)
	}
	defer resp.Body.Close()

	var pr porkbunResp
	json.NewDecoder(resp.Body).Decode(&pr)
	if pr.Status != "SUCCESS" {
		return nil, fmt.Errorf("porkbun: retrieve failed")
	}

	result := make([]dns.Record, 0, len(pr.Records))
	for _, r := range pr.Records {
		ttl := 0
		if r.TTL != "" {
			fmt.Sscanf(r.TTL, "%d", &ttl)
		}
		result = append(result, dns.Record{
			ID:    r.ID,
			Name:  r.Name,
			Type:  r.Type,
			Value: r.Content,
			TTL:   ttl,
		})
	}
	return result, nil
}
