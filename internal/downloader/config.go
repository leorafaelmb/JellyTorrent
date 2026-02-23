package downloader

import "time"

type Config struct {
	MaxWorkers    int
	MaxRetries    int
	PipelineDepth int
	Timeout       time.Duration
	Verbose       bool
	PieceSelector PieceSelector
}

func DefaultConfig() Config {
	return Config{
		MaxWorkers: 50,
		MaxRetries: 3,
		Timeout:    5 * time.Minute,
		Verbose:    false,
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

func WithVerbose(verbose bool) Option {
	return func(c *Config) {
		c.Verbose = verbose
	}
}

func WithPieceSelector(s PieceSelector) Option {
	return func(c *Config) {
		c.PieceSelector = s
	}
}
