package server

import (
	"context"
	"sync"
	"time"
)

const providerMonitoringCacheTTL = 15 * time.Second
const providerMonitoringQueryTimeout = 10 * time.Second

type monitoringObservationFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	data    map[string]ProviderObservationSummary
	err     error
}

type monitoringObservationCache struct {
	mu      sync.Mutex
	key     string
	data    map[string]ProviderObservationSummary
	expires time.Time
	flights map[string]*monitoringObservationFlight
}

// Concurrent refreshes share a bounded query. Individual cancellation stops
// waiting immediately; the last departing waiter also cancels database work.
func (c *monitoringObservationCache) get(ctx context.Context, key string, load func(context.Context) (map[string]ProviderObservationSummary, error)) (map[string]ProviderObservationSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.key == key && c.data != nil && time.Now().Before(c.expires) {
		data := c.data
		c.mu.Unlock()
		return data, nil
	}
	if c.flights == nil {
		c.flights = make(map[string]*monitoringObservationFlight)
	}
	flight := c.flights[key]
	if flight == nil {
		queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerMonitoringQueryTimeout)
		flight = &monitoringObservationFlight{done: make(chan struct{}), cancel: cancel}
		c.flights[key] = flight
		go func() {
			data, err := load(queryCtx)
			if err == nil {
				err = queryCtx.Err()
			}
			cancel()
			c.mu.Lock()
			flight.data, flight.err = data, err
			if c.flights[key] == flight {
				delete(c.flights, key)
				if err == nil {
					c.key, c.data, c.expires = key, data, time.Now().Add(providerMonitoringCacheTTL)
				}
			}
			close(flight.done)
			c.mu.Unlock()
		}()
	}
	flight.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 && c.flights[key] == flight {
			delete(c.flights, key)
			flight.cancel()
		}
		c.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-flight.done:
		return flight.data, flight.err
	}
}
