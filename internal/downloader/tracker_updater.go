package downloader

import (
	"context"
	"time"

	"github.com/leorafaelmb/BitTorrent-Client/internal/logger"
	"github.com/leorafaelmb/BitTorrent-Client/internal/tracker"
)

// TrackerUpdater periodically re-announces to the tracker during download
// and sends completed/stopped events at the appropriate times.
type TrackerUpdater struct {
	tracker     tracker.Tracker
	announceReq tracker.AnnounceRequest
	pm          *PieceManager
	totalLength int
	pieceLength int
}

// NewTrackerUpdater creates a new tracker updater.
func NewTrackerUpdater(tr tracker.Tracker, req tracker.AnnounceRequest, pm *PieceManager, totalLength, pieceLength int) *TrackerUpdater {
	return &TrackerUpdater{
		tracker:     tr,
		announceReq: req,
		pm:          pm,
		totalLength: totalLength,
		pieceLength: pieceLength,
	}
}

// Run starts the periodic re-announce loop. It blocks until the context is cancelled
// or the download completes. Should be run in a goroutine.
func (tu *TrackerUpdater) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			tu.sendStopped()
			return

		case <-tu.pm.Done():
			tu.sendCompleted()
			return

		case <-tick.C:
			newInterval := tu.sendUpdate()
			if newInterval > 0 && newInterval != interval {
				interval = newInterval
				tick.Reset(interval)
				logger.Log.Debug("tracker interval updated", "interval", interval)
			}
		}
	}
}

// computeLeft returns the number of bytes remaining to download.
func (tu *TrackerUpdater) computeLeft() (downloaded uint64, left uint64) {
	completed, _ := tu.pm.Progress()
	downloadedBytes := uint64(completed) * uint64(tu.pieceLength)
	if downloadedBytes > uint64(tu.totalLength) {
		downloadedBytes = uint64(tu.totalLength)
	}
	return downloadedBytes, uint64(tu.totalLength) - downloadedBytes
}

func (tu *TrackerUpdater) sendUpdate() time.Duration {
	downloaded, left := tu.computeLeft()

	req := tu.announceReq
	req.Event = tracker.EventNone
	req.Downloaded = downloaded
	req.Left = left

	resp, err := tu.tracker.Announce(req)
	if err != nil {
		logger.Log.Debug("tracker re-announce failed", "error", err)
		return 0
	}

	logger.Log.Debug("tracker re-announce", "downloaded", downloaded, "left", left, "peers", len(resp.Peers))
	return resp.Interval
}

func (tu *TrackerUpdater) sendCompleted() {
	req := tu.announceReq
	req.Event = tracker.EventCompleted
	req.Downloaded = uint64(tu.totalLength)
	req.Left = 0

	if _, err := tu.tracker.Announce(req); err != nil {
		logger.Log.Debug("tracker completed announce failed", "error", err)
		return
	}
	logger.Log.Info("tracker notified of completion")
}

func (tu *TrackerUpdater) sendStopped() {
	downloaded, left := tu.computeLeft()

	req := tu.announceReq
	req.Event = tracker.EventStopped
	req.Downloaded = downloaded
	req.Left = left

	if _, err := tu.tracker.Announce(req); err != nil {
		logger.Log.Debug("tracker stopped announce failed", "error", err)
		return
	}
	logger.Log.Debug("tracker notified of stop")
}
