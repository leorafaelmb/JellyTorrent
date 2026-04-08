package dht

import (
	"io"
	"log/slog"
	"net/netip"
	"time"

	"github.com/leorafaelmb/JellyTorrent/internal/dht/routing"
)

// BEP42Mode controls how BEP 42 node ID restrictions are enforced.
type BEP42Mode int

const (
	BEP42Off       BEP42Mode = iota // No validation (default)
	BEP42Log                        // Validate and log, insert all nodes
	BEP42Enforce                    // Reject non-compliant nodes
	BEP42TableOnly                  // Validate for table insertion, respond to all queries
)

type Config struct {
	Port             int
	BootstrapNodes   []string
	Alpha            int
	K                int
	PeerTTL          time.Duration
	Logger           *slog.Logger
	TableLogger      *slog.Logger
	RoutingTablePath string
	ExternalIP       netip.Addr
	BEP42            BEP42Mode
	RateLimit        int           // max queries per window per IP (0 = disabled)
	RateLimitWin     time.Duration // rate limit window duration
	MetricsPort      int           // HTTP port for /metrics endpoint (0 = disabled)
}

func DefaultConfig() Config {
	return Config{
		Port: 6881,
		BootstrapNodes: []string{
			"router.bittorrent.com:6881",
			"dht.transmissionbt.com:6881",
			"router.utorrent.com:6881",
		},
		Alpha:   3,
		K:       routing.K,
		PeerTTL: 30 * time.Minute,
		Logger:       slog.Default(),
		TableLogger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		RateLimit:    50,
		RateLimitWin: 1 * time.Minute,
	}
}

type Option func(*Config)

func WithPort(port int) Option {
	return func(c *Config) {
		if port >= 0 {
			c.Port = port
		}
	}
}

func WithBootstrapNodes(nodes []string) Option {
	return func(c *Config) {
		c.BootstrapNodes = nodes
	}
}

func WithAlpha(alpha int) Option {
	return func(c *Config) {
		if alpha > 0 {
			c.Alpha = alpha
		}
	}
}

func WithLogger(l *slog.Logger) Option {
	return func(c *Config) {
		c.Logger = l
	}
}

func WithRoutingTable(path string) Option {
	return func(c *Config) {
		c.RoutingTablePath = path
	}
}

func WithBEP42(mode BEP42Mode) Option {
	return func(c *Config) {
		c.BEP42 = mode
	}
}

func WithMetricsPort(port int) Option {
	return func(c *Config) {
		c.MetricsPort = port
	}
}

func WithRateLimit(limit int, window time.Duration) Option {
	return func(c *Config) {
		c.RateLimit = limit
		if window > 0 {
			c.RateLimitWin = window
		}
	}
}

func WithExternalIP(ip netip.Addr) Option {
	return func(c *Config) {
		c.ExternalIP = ip
	}
}

func WithTableLogger(l *slog.Logger) Option {
	return func(c *Config) {
		c.TableLogger = l
	}
}

