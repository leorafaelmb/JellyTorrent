package dht

import (
	"github.com/leorafaelmb/JellyTorrent/internal/dht/routing"
	"log/slog"
	"time"
)

type Config struct {
	Port             int
	BootstrapNodes   []string
	Alpha            int
	K                int
	PeerTTL          time.Duration
	Logger           *slog.Logger
	RoutingTablePath string
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
		Logger:  slog.Default(),
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
