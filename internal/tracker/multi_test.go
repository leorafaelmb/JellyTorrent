package tracker

import (
	"fmt"
	"testing"
	"time"
)

type mockTracker struct {
	announceResp AnnounceResponse
	announceErr  error
	scrapeResp   ScrapeFiles
	scrapeErr    error
}

func (m *mockTracker) Announce(req AnnounceRequest) (AnnounceResponse, error) {
	return m.announceResp, m.announceErr
}

func (m *mockTracker) Scrape(infoHashes [][20]byte) (ScrapeFiles, error) {
	return m.scrapeResp, m.scrapeErr
}

func TestMultiTracker_Fallback(t *testing.T) {
	failing := &mockTracker{announceErr: fmt.Errorf("connection refused")}
	succeeding := &mockTracker{
		announceResp: AnnounceResponse{Interval: 30 * time.Minute},
	}

	mt := &MultiTracker{trackers: []Tracker{failing, succeeding}}

	resp, err := mt.Announce(AnnounceRequest{})
	if err != nil {
		t.Fatalf("expected success after fallback, got error: %v", err)
	}
	if resp.Interval != 30*time.Minute {
		t.Fatalf("expected interval from second tracker, got %v", resp.Interval)
	}
}

func TestMultiTracker_AllFail(t *testing.T) {
	failing1 := &mockTracker{announceErr: fmt.Errorf("timeout")}
	failing2 := &mockTracker{announceErr: fmt.Errorf("connection refused")}

	mt := &MultiTracker{trackers: []Tracker{failing1, failing2}}

	_, err := mt.Announce(AnnounceRequest{})
	if err == nil {
		t.Fatal("expected error when all trackers fail")
	}
}

func TestMultiTracker_FirstSucceeds(t *testing.T) {
	first := &mockTracker{
		announceResp: AnnounceResponse{Interval: 15 * time.Minute},
	}
	second := &mockTracker{
		announceResp: AnnounceResponse{Interval: 30 * time.Minute},
	}

	mt := &MultiTracker{trackers: []Tracker{first, second}}

	resp, err := mt.Announce(AnnounceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should use first tracker's response, not fall through
	if resp.Interval != 15*time.Minute {
		t.Fatalf("expected interval from first tracker (15m), got %v", resp.Interval)
	}
}

func TestMultiTracker_ScrapeFallback(t *testing.T) {
	failing := &mockTracker{scrapeErr: fmt.Errorf("not supported")}
	succeeding := &mockTracker{scrapeResp: ScrapeFiles{}}

	mt := &MultiTracker{trackers: []Tracker{failing, succeeding}}

	_, err := mt.Scrape(nil)
	if err != nil {
		t.Fatalf("expected scrape success after fallback, got: %v", err)
	}
}

func TestNewMultiTracker_NoValidURLs(t *testing.T) {
	_, err := NewMultiTracker([]string{"ftp://invalid.example.com"})
	if err == nil {
		t.Fatal("expected error for no valid trackers")
	}
}
