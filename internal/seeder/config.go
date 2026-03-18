package seeder

import (
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal"
)

// Config holds seeder configuration.
type Config struct {
	ListenPort      int
	MaxPeers        int
	UnchokeSlots    int
	UnchokeInterval time.Duration
}

// DefaultConfig returns the default seeder configuration.
func DefaultConfig() Config {
	return Config{
		ListenPort:      internal.DefaultPort,
		MaxPeers:        internal.DefaultMaxSeedPeers,
		UnchokeSlots:    internal.DefaultUnchokeSlots,
		UnchokeInterval: internal.DefaultUnchokeInterval,
	}
}

// Option configures the seeder.
type Option func(*Config)

// WithListenPort sets the TCP listen port.
func WithListenPort(port int) Option {
	return func(c *Config) { c.ListenPort = port }
}

// WithMaxPeers sets the maximum number of concurrent seed connections.
func WithMaxPeers(n int) Option {
	return func(c *Config) { c.MaxPeers = n }
}

// WithUnchokeSlots sets the number of simultaneously unchoked peers.
func WithUnchokeSlots(n int) Option {
	return func(c *Config) { c.UnchokeSlots = n }
}

// WithUnchokeInterval sets how often the choker rotates unchoked peers.
func WithUnchokeInterval(d time.Duration) Option {
	return func(c *Config) { c.UnchokeInterval = d }
}
