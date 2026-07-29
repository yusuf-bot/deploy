package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockTransport wraps a handler to create a round tripper
type mockTransport struct {
	handler http.Handler
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	m.handler.ServeHTTP(w, req)
	return w.Result(), nil
}

func TestCloudflareProvider(t *testing.T) {
	mux := http.NewServeMux()
	zoneID := "abc123zone"

	// Zone lookup
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "example.com" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"abc123zone","name":"example.com"}]}`))
	})

	// List records (existing record with matching value)
	mux.HandleFunc("/zones/"+zoneID+"/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"rec456","name":"@","type":"A","content":"1.2.3.4","ttl":300}]}`))
		case http.MethodPost:
			w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"rec789","name":"@","type":"A","content":"5.6.7.8","ttl":300}}`))
		}
	})

	// Delete
	mux.HandleFunc("/zones/"+zoneID+"/dns_records/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &cloudflareProvider{
		token:   "test-token",
		baseURL: server.URL,
		client:  &http.Client{Transport: &mockTransport{mux}},
	}

	t.Run("Name", func(t *testing.T) {
		if n := p.Name(); n != "cloudflare" {
			t.Errorf("Name() = %q, want %q", n, "cloudflare")
		}
	})

	t.Run("ListRecords", func(t *testing.T) {
		records, err := p.ListRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
		if records[0].ID != "rec456" {
			t.Errorf("record ID = %q, want %q", records[0].ID, "rec456")
		}
	})

	t.Run("EnsureRecord_Exists", func(t *testing.T) {
		id, existed, err := p.EnsureRecord(context.Background(), "example.com", "@", "A", "1.2.3.4", 300)
		if err != nil {
			t.Fatalf("EnsureRecord: %v", err)
		}
		if !existed {
			t.Errorf("existed = false, want true")
		}
		if id != "rec456" {
			t.Errorf("id = %q, want %q", id, "rec456")
		}
	})

	t.Run("EnsureRecord_Create", func(t *testing.T) {
		id, existed, err := p.EnsureRecord(context.Background(), "example.com", "@", "A", "5.6.7.8", 300)
		if err != nil {
			t.Fatalf("EnsureRecord: %v", err)
		}
		if existed {
			t.Errorf("existed = true, want false")
		}
		if id != "rec789" {
			t.Errorf("id = %q, want %q", id, "rec789")
		}
	})

	t.Run("DeleteRecord", func(t *testing.T) {
		err := p.DeleteRecord(context.Background(), "example.com", "rec456")
		if err != nil {
			t.Fatalf("DeleteRecord: %v", err)
		}
	})
}

func TestDigitalOceanProvider(t *testing.T) {
	mux := http.NewServeMux()
	recordID := 456

	mux.HandleFunc("/v2/domains/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/records") && r.Method == http.MethodGet:
			w.Write([]byte(fmt.Sprintf(`{"domain_records":[{"id":%d,"type":"A","name":"@","data":"1.2.3.4","ttl":300}]}`, recordID)))
		case strings.HasSuffix(path, "/records") && r.Method == http.MethodPost:
			w.Write([]byte(fmt.Sprintf(`{"domain_record":{"id":%d,"type":"A","name":"@","data":"5.6.7.8","ttl":300}}`, recordID+1)))
		case strings.Contains(path, "/records/") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &doProvider{
		token:   "test-token",
		baseURL: server.URL,
		client:  &http.Client{Transport: &mockTransport{mux}},
	}

	t.Run("Name", func(t *testing.T) {
		if n := p.Name(); n != "digitalocean" {
			t.Errorf("Name() = %q, want %q", n, "digitalocean")
		}
	})

	t.Run("ListRecords", func(t *testing.T) {
		records, err := p.ListRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
	})

	t.Run("EnsureRecord_Exists", func(t *testing.T) {
		_, existed, err := p.EnsureRecord(context.Background(), "example.com", "@", "A", "1.2.3.4", 300)
		if err != nil {
			t.Fatalf("EnsureRecord: %v", err)
		}
		if !existed {
			t.Errorf("existed = false, want true")
		}
	})

	t.Run("DeleteRecord", func(t *testing.T) {
		err := p.DeleteRecord(context.Background(), "example.com", "456")
		if err != nil {
			t.Fatalf("DeleteRecord: %v", err)
		}
	})
}

func TestHetznerProvider(t *testing.T) {
	mux := http.NewServeMux()
	zoneID := 123

	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "example.com" {
			w.Write([]byte(`{"zones":[]}`))
			return
		}
		w.Write([]byte(fmt.Sprintf(`{"zones":[{"id":%d,"name":"example.com"}]}`, zoneID)))
	})

	mux.HandleFunc("/zones/123/rrsets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"rrsets":[{"id":"@/A","name":"@","type":"A","ttl":300,"records":[{"value":"1.2.3.4"}]}]}`))
		} else if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"rrset":{"id":"@/A","name":"@","type":"A","ttl":300,"records":[{"value":"5.6.7.8"}]}}`))
		}
	})

	mux.HandleFunc("/zones/123/rrsets/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/actions/set_records") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// GET single rrset
		w.Write([]byte(`{"rrset":{"id":"@/A","name":"@","type":"A","ttl":300,"records":[{"value":"1.2.3.4"}]}}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &hetznerProvider{
		token:   "test-token",
		baseURL: server.URL,
		client:  &http.Client{Transport: &mockTransport{mux}},
	}

	t.Run("Name", func(t *testing.T) {
		if n := p.Name(); n != "hetzner" {
			t.Errorf("Name() = %q, want %q", n, "hetzner")
		}
	})

	t.Run("ListRecords", func(t *testing.T) {
		records, err := p.ListRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
	})

	t.Run("EnsureRecord_Exists", func(t *testing.T) {
		_, existed, err := p.EnsureRecord(context.Background(), "example.com", "@", "A", "1.2.3.4", 300)
		if err != nil {
			t.Fatalf("EnsureRecord: %v", err)
		}
		if !existed {
			t.Errorf("existed = false, want true")
		}
	})

	t.Run("DeleteRecord", func(t *testing.T) {
		err := p.DeleteRecord(context.Background(), "example.com", "@/A")
		if err != nil {
			t.Fatalf("DeleteRecord: %v", err)
		}
	})
}

func TestLinodeProvider(t *testing.T) {
	mux := http.NewServeMux()
	domainID := 789
	recordID := 101112

	mux.HandleFunc("/domains", func(w http.ResponseWriter, r *http.Request) {
		// Only handles the list domains endpoint
		if r.URL.Path == "/domains" && r.Method == http.MethodGet {
			w.Write([]byte(fmt.Sprintf(`{"data":[{"id":%d,"domain":"example.com"}]}`, domainID)))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/domains/789/records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(fmt.Sprintf(`{"data":[{"id":%d,"type":"A","name":"@","target":"1.2.3.4","ttl_sec":300}]}`, recordID)))
		case http.MethodPost:
			w.Write([]byte(fmt.Sprintf(`{"data":{"id":%d,"type":"A","name":"@","target":"5.6.7.8","ttl_sec":300}}`, recordID+1)))
		}
	})

	mux.HandleFunc("/domains/789/records/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &linodeProvider{
		token:   "test-token",
		baseURL: server.URL,
		client:  &http.Client{Transport: &mockTransport{mux}},
	}

	t.Run("Name", func(t *testing.T) {
		if n := p.Name(); n != "linode" {
			t.Errorf("Name() = %q, want %q", n, "linode")
		}
	})

	t.Run("ListRecords", func(t *testing.T) {
		records, err := p.ListRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
	})

	t.Run("EnsureRecord_Exists", func(t *testing.T) {
		_, existed, err := p.EnsureRecord(context.Background(), "example.com", "@", "A", "1.2.3.4", 300)
		if err != nil {
			t.Fatalf("EnsureRecord: %v", err)
		}
		if !existed {
			t.Errorf("existed = false, want true")
		}
	})

	t.Run("DeleteRecord", func(t *testing.T) {
		err := p.DeleteRecord(context.Background(), "example.com", "101112")
		if err != nil {
			t.Fatalf("DeleteRecord: %v", err)
		}
	})
}

func TestVultrProvider(t *testing.T) {
	mux := http.NewServeMux()
	recordID := "vultr-rec-1"

	mux.HandleFunc("/domains/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/records") && r.Method == http.MethodGet:
			w.Write([]byte(fmt.Sprintf(`{"records":[{"id":"%s","type":"A","name":"@","data":"1.2.3.4","ttl":300}]}`, recordID)))
		case strings.HasSuffix(path, "/records") && r.Method == http.MethodPost:
			w.Write([]byte(fmt.Sprintf(`{"record":{"id":"%s","type":"A","name":"@","data":"5.6.7.8","ttl":300}}`, recordID+"-new")))
		case strings.Contains(path, "/records/") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &vultrProvider{
		token:   "test-token",
		baseURL: server.URL,
		client:  &http.Client{Transport: &mockTransport{mux}},
	}

	t.Run("Name", func(t *testing.T) {
		if n := p.Name(); n != "vultr" {
			t.Errorf("Name() = %q, want %q", n, "vultr")
		}
	})

	t.Run("ListRecords", func(t *testing.T) {
		records, err := p.ListRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
	})

	t.Run("EnsureRecord_Exists", func(t *testing.T) {
		_, existed, err := p.EnsureRecord(context.Background(), "example.com", "@", "A", "1.2.3.4", 300)
		if err != nil {
			t.Fatalf("EnsureRecord: %v", err)
		}
		if !existed {
			t.Errorf("existed = false, want true")
		}
	})

	t.Run("DeleteRecord", func(t *testing.T) {
		err := p.DeleteRecord(context.Background(), "example.com", recordID)
		if err != nil {
			t.Fatalf("DeleteRecord: %v", err)
		}
	})
}

func TestPorkbunProvider(t *testing.T) {
	mux := http.NewServeMux()
	recordID := "porkbun-rec-1"

	mux.HandleFunc("/dns/retrieve/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf(`{"status":"SUCCESS","records":[{"id":"%s","name":"@","type":"A","content":"1.2.3.4","ttl":"300","prio":"0"}]}`, recordID)))
	})

	mux.HandleFunc("/dns/create/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf(`{"status":"SUCCESS","id":"%s"}`, recordID+"-new")))
	})

	mux.HandleFunc("/dns/delete/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"SUCCESS"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	p := &porkbunProvider{
		apiKey:    "test-api-key",
		secretKey: "test-secret-key",
		baseURL:   server.URL,
		client:    &http.Client{Transport: &mockTransport{mux}},
	}

	t.Run("Name", func(t *testing.T) {
		if n := p.Name(); n != "porkbun" {
			t.Errorf("Name() = %q, want %q", n, "porkbun")
		}
	})

	t.Run("ListRecords", func(t *testing.T) {
		records, err := p.ListRecords(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
	})

	t.Run("EnsureRecord_Exists", func(t *testing.T) {
		_, existed, err := p.EnsureRecord(context.Background(), "example.com", "@", "A", "1.2.3.4", 300)
		if err != nil {
			t.Fatalf("EnsureRecord: %v", err)
		}
		if !existed {
			t.Errorf("existed = false, want true")
		}
	})

	t.Run("DeleteRecord", func(t *testing.T) {
		err := p.DeleteRecord(context.Background(), "example.com", recordID)
		if err != nil {
			t.Fatalf("DeleteRecord: %v", err)
		}
	})
}

// TestZoneNotFound verifies providers handle missing zones gracefully
func TestZoneNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("Cloudflare", func(t *testing.T) {
		p := &cloudflareProvider{
			token:   "test",
			baseURL: server.URL,
			client:  &http.Client{Transport: &mockTransport{mux}},
		}
		_, err := p.ListRecords(context.Background(), "nonexistent.com")
		if err == nil {
			t.Error("expected error for nonexistent zone")
		}
	})

	t.Run("Hetzner", func(t *testing.T) {
		p := &hetznerProvider{
			token:   "test",
			baseURL: server.URL,
			client:  &http.Client{Transport: &mockTransport{mux}},
		}
		_, err := p.ListRecords(context.Background(), "nonexistent.com")
		if err == nil {
			t.Error("expected error for nonexistent zone")
		}
	})

	t.Run("Linode", func(t *testing.T) {
		p := &linodeProvider{
			token:   "test",
			baseURL: server.URL,
			client:  &http.Client{Transport: &mockTransport{mux}},
		}
		_, err := p.ListRecords(context.Background(), "nonexistent.com")
		if err == nil {
			t.Error("expected error for nonexistent zone")
		}
	})
}
