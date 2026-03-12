package downloader

import (
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/tracker"
)

type Config struct {
	MaxWorkers    int
	MaxRetries    int
	PipelineDepth int
	Timeout       time.Duration
	PieceSelector PieceSelector
	Tracker           tracker.Tracker
	AnnounceReq       tracker.AnnounceRequest
	AnnounceInterval  time.Duration
	StorageDir        string
}

func DefaultConfig() Config {
	return Config{
		MaxWorkers:    50,
		MaxRetries:    3,
		Timeout:       50 * time.Minute,
		PieceSelector: &RarestFirstSelector{},
	}
}

type Option func(*Config)

func WithMaxWorkers(n int) Option {
	return func(c *Config) {
		if n > 0 {
			c.MaxWorkers = n
		}
	}
}

func WithMaxRetries(n int) Option {
	return func(c *Config) {
		if n > 0 {
			c.MaxRetries = n
		}
	}
}

func WithPieceSelector(s PieceSelector) Option {
	return func(c *Config) {
		c.PieceSelector = s
	}
}

func WithTracker(tr tracker.Tracker, req tracker.AnnounceRequest) Option {
	return func(c *Config) {
		c.Tracker = tr
		c.AnnounceReq = req
	}
}

func WithAnnounceInterval(d time.Duration) Option {
	return func(c *Config) {
		c.AnnounceInterval = d
	}
}

func WithStorageDir(dir string) Option {
	return func(c *Config) {
		c.StorageDir = dir
	}
}
