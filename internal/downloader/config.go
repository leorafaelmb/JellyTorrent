package downloader

import "time"

type Config struct {
	MaxWorkers    int
	MaxRetries    int
	PipelineDepth int
	Timeout       time.Duration
	PieceSelector PieceSelector
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
