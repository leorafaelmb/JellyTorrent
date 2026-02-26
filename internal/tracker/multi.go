package tracker

import (
	"fmt"

	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
)

// MultiTracker implements the Tracker interface by trying multiple trackers
// in order, falling back to the next on failure.
type MultiTracker struct {
	trackers []Tracker
}

// Compile-time check that MultiTracker implements Tracker.
var _ Tracker = (*MultiTracker)(nil)

// NewMultiTracker creates a MultiTracker from a list of tracker URLs.
// Skips URLs with unsupported schemes. Returns an error only if no
// valid trackers could be created.
func NewMultiTracker(urls []string) (*MultiTracker, error) {
	var trackers []Tracker
	for _, u := range urls {
		tr, err := NewTracker(u)
		if err != nil {
			logger.Log.Debug("skipping tracker", "url", u, "error", err)
			continue
		}
		trackers = append(trackers, tr)
	}
	if len(trackers) == 0 {
		return nil, fmt.Errorf("no valid trackers from %d URLs", len(urls))
	}
	logger.Log.Info("multi-tracker initialized", "trackers", len(trackers), "total_urls", len(urls))
	return &MultiTracker{trackers: trackers}, nil
}

func (mt *MultiTracker) Announce(req AnnounceRequest) (AnnounceResponse, error) {
	var lastErr error
	for _, tr := range mt.trackers {
		resp, err := tr.Announce(req)
		if err != nil {
			logger.Log.Debug("tracker announce failed, trying next", "error", err)
			lastErr = err
			continue
		}
		return resp, nil
	}
	return AnnounceResponse{}, fmt.Errorf("all trackers failed: %w", lastErr)
}

func (mt *MultiTracker) Scrape(infoHashes [][20]byte) (ScrapeFiles, error) {
	var lastErr error
	for _, tr := range mt.trackers {
		resp, err := tr.Scrape(infoHashes)
		if err != nil {
			logger.Log.Debug("tracker scrape failed, trying next", "error", err)
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("all trackers failed: %w", lastErr)
}
