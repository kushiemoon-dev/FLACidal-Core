package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRaceRequest_FirstSuccessWins(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"source":"slow"}`))
	}))
	defer slow.Close()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"source":"fast"}`))
	}))
	defer fast.Close()

	pool := NewEndpointPool([]string{slow.URL, fast.URL}, 5*time.Minute)

	result, err := pool.RaceRequest(context.Background(), "/test")
	if err != nil {
		t.Fatalf("RaceRequest failed: %v", err)
	}
	if string(result.Body) != `{"source":"fast"}` {
		t.Errorf("expected fast server response, got: %s", result.Body)
	}
	if result.Endpoint != fast.URL {
		t.Errorf("expected endpoint %s, got %s", fast.URL, result.Endpoint)
	}
}

func TestRaceRequest_BlacklistsFailedEndpoints(t *testing.T) {
	// Failing server responds with 500 immediately.
	// Working server has a small delay so the 500 result is always processed before the winner is found.
	// This makes the blacklisting test deterministic.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer failing.Close()

	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond) // ensures failing endpoint is processed first
		w.Write([]byte(`ok`))
	}))
	defer working.Close()

	pool := NewEndpointPool([]string{failing.URL, working.URL}, 5*time.Minute)

	_, err := pool.RaceRequest(context.Background(), "/test")
	if err != nil {
		t.Fatalf("RaceRequest failed: %v", err)
	}

	healthy := pool.GetHealthy()
	if len(healthy) != 1 || healthy[0] != working.URL {
		t.Errorf("expected only working endpoint healthy, got: %v", healthy)
	}
}

func TestRaceRequest_AllEndpointsFail(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer failing.Close()

	pool := NewEndpointPool([]string{failing.URL}, 5*time.Minute)

	_, err := pool.RaceRequest(context.Background(), "/test")
	if err == nil {
		t.Fatal("expected error when all endpoints fail")
	}
}

func TestSequentialRequest_SkipsFailedEndpoints(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer failing.Close()

	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`ok`))
	}))
	defer working.Close()

	pool := NewEndpointPool([]string{failing.URL, working.URL}, 5*time.Minute)

	result, err := pool.SequentialRequest(context.Background(), "/test")
	if err != nil {
		t.Fatalf("SequentialRequest failed: %v", err)
	}
	if string(result.Body) != "ok" {
		t.Errorf("expected 'ok', got: %s", result.Body)
	}
}

func TestBlacklistExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`ok`))
	}))
	defer server.Close()

	pool := NewEndpointPool([]string{server.URL}, 50*time.Millisecond)
	pool.Blacklist(server.URL)

	healthy := pool.GetHealthy()
	if len(healthy) != 0 {
		t.Error("expected no healthy endpoints immediately after blacklist")
	}

	time.Sleep(60 * time.Millisecond)
	healthy = pool.GetHealthy()
	if len(healthy) != 1 {
		t.Errorf("expected 1 healthy endpoint after expiry, got %d", len(healthy))
	}
}

func TestGetAvailableAndBlacklist(t *testing.T) {
	pool := NewEndpointPool([]string{"http://a.test", "http://b.test"}, 5*time.Minute)
	pool.Blacklist("http://a.test")

	available := pool.GetAvailable()
	// Should have b first (active), then a (blacklisted but included as last resort)
	if len(available) != 2 {
		t.Errorf("expected 2 available (active + blacklisted fallback), got %d", len(available))
	}
	if available[0] != "http://b.test" {
		t.Errorf("expected active endpoint first, got: %s", available[0])
	}
}

func TestSetEndpoints(t *testing.T) {
	pool := NewEndpointPool([]string{"http://old.test"}, 5*time.Minute)
	pool.SetEndpoints([]string{"http://new1.test", "http://new2.test"})

	available := pool.GetAvailable()
	if len(available) != 2 {
		t.Errorf("expected 2 endpoints after SetEndpoints, got %d", len(available))
	}
	if available[0] != "http://new1.test" {
		t.Errorf("unexpected first endpoint: %s", available[0])
	}
}
