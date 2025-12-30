package buffer

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/codetrek/syntrix/internal/events"
)

// Cleaner periodically removes old events from the buffer.
type Cleaner struct {
	buffer    *Buffer
	retention time.Duration
	interval  time.Duration
	logger    *slog.Logger

	// wg tracks the cleanup goroutine
	wg sync.WaitGroup

	// done signals shutdown
	done chan struct{}
}

// CleanerOptions configures the cleaner.
type CleanerOptions struct {
	// Buffer to clean.
	Buffer *Buffer

	// Retention is how long to keep events.
	Retention time.Duration

	// Interval is how often to run cleanup.
	Interval time.Duration

	// Logger for cleaner operations.
	Logger *slog.Logger
}

// NewCleaner creates a new cleaner.
func NewCleaner(opts CleanerOptions) *Cleaner {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Cleaner{
		buffer:    opts.Buffer,
		retention: opts.Retention,
		interval:  opts.Interval,
		logger:    logger.With("component", "event-cleaner"),
		done:      make(chan struct{}),
	}
}

// Start starts the cleaner goroutine.
func (c *Cleaner) Start(ctx context.Context) {
	c.wg.Add(1)
	go c.run(ctx)
	c.logger.Info("cleaner started", "retention", c.retention, "interval", c.interval)
}

// Stop stops the cleaner and waits for it to finish.
func (c *Cleaner) Stop() {
	close(c.done)
	c.wg.Wait()
	c.logger.Info("cleaner stopped")
}

func (c *Cleaner) run(ctx context.Context) {
	defer c.wg.Done()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
			if err := c.cleanup(ctx); err != nil {
				c.logger.Error("cleanup failed", "error", err)
			}
		}
	}
}

// cleanup removes events older than the retention period.
func (c *Cleaner) cleanup(ctx context.Context) error {
	// Calculate the cutoff time
	cutoff := time.Now().Add(-c.retention)
	cutoffKey := events.FormatBufferKey(events.ClusterTime{
		T: uint32(cutoff.Unix()),
		I: 0,
	}, "")

	count, err := c.buffer.DeleteBefore(cutoffKey)
	if err != nil {
		return err
	}

	if count > 0 {
		c.logger.Debug("cleaned up old events", "count", count)
	}

	return nil
}

// CleanupNow runs cleanup immediately.
func (c *Cleaner) CleanupNow(ctx context.Context) error {
	return c.cleanup(ctx)
}
