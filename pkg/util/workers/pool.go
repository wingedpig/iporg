package workers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Task represents a unit of work to be executed
type Task func(ctx context.Context) error

// Result contains the result of a task execution
type Result struct {
	Index int   // Index of the task in the input slice
	Error error // Error if task failed
}

// Pool represents a worker pool with rate limiting
type Pool struct {
	workers     int
	limiter     *rate.Limiter
	semaphore   chan struct{}
	results     chan Result
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

// Config contains configuration for a worker pool
type Config struct {
	Workers       int     // Number of concurrent workers
	RateLimit     float64 // Requests per second (0 = no limit)
	BurstSize     int     // Burst size for rate limiter
}

// NewPool creates a new worker pool
func NewPool(ctx context.Context, cfg Config) *Pool {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.BurstSize <= 0 {
		cfg.BurstSize = cfg.Workers
	}

	poolCtx, cancel := context.WithCancel(ctx)

	var limiter *rate.Limiter
	if cfg.RateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(cfg.RateLimit), cfg.BurstSize)
	}

	return &Pool{
		workers:   cfg.Workers,
		limiter:   limiter,
		semaphore: make(chan struct{}, cfg.Workers),
		results:   make(chan Result, cfg.Workers*2), // Buffered to avoid blocking
		ctx:       poolCtx,
		cancel:    cancel,
	}
}

// Submit submits a task to the worker pool
func (p *Pool) Submit(index int, task Task) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		// Acquire semaphore (limit concurrency)
		select {
		case p.semaphore <- struct{}{}:
			defer func() { <-p.semaphore }()
		case <-p.ctx.Done():
			p.results <- Result{Index: index, Error: p.ctx.Err()}
			return
		}

		// Wait for rate limiter
		if p.limiter != nil {
			if err := p.limiter.Wait(p.ctx); err != nil {
				p.results <- Result{Index: index, Error: err}
				return
			}
		}

		// Execute task
		err := task(p.ctx)
		p.results <- Result{Index: index, Error: err}
	}()
}

// Wait waits for all tasks to complete and returns results
func (p *Pool) Wait() []Result {
	// Close results channel when all workers are done
	go func() {
		p.wg.Wait()
		close(p.results)
	}()

	// Collect results
	var results []Result
	for result := range p.results {
		results = append(results, result)
	}

	return results
}

// Stop cancels all pending tasks
func (p *Pool) Stop() {
	p.cancel()
}

// RetryConfig contains configuration for retry logic
type RetryConfig struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}
}

// Retry executes a function with exponential backoff
func Retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if attempt == cfg.MaxAttempts {
			break
		}

		// Exponential backoff with jitter
		select {
		case <-time.After(delay):
			delay = time.Duration(float64(delay) * cfg.Multiplier)
			if delay > cfg.MaxDelay {
				delay = cfg.MaxDelay
			}
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		}
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// RateLimitedRetry combines rate limiting and retry logic
func RateLimitedRetry(ctx context.Context, limiter *rate.Limiter, cfg RetryConfig, fn func() error) error {
	return Retry(ctx, cfg, func() error {
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				return err
			}
		}
		return fn()
	})
}
