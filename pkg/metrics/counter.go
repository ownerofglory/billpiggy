package metrics

import "sync"

// Counter is a monotonically increasing value.
type Counter struct {
	mu    sync.Mutex
	value float64
}

// Inc increments the counter by one.
func (c *Counter) Inc() { c.Add(1) }

// Add increments the counter by delta, which must be non-negative.
func (c *Counter) Add(delta float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value += delta
}

// Value returns the current total.
func (c *Counter) Value() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// CounterVec is a set of counters distinguished by a fixed list of label
// values, such as {route, method, status} for an HTTP request counter.
type CounterVec struct {
	mu       sync.Mutex
	counters map[string]*Counter
	seen     [][]string
}

func newCounterVec() *CounterVec {
	return &CounterVec{counters: map[string]*Counter{}}
}

// WithLabelValues returns the counter for this exact combination of label
// values, creating it on first use. Values must be supplied in the order the
// vector's label names were declared.
func (v *CounterVec) WithLabelValues(values ...string) *Counter {
	key := labelKey(values)
	v.mu.Lock()
	defer v.mu.Unlock()
	counter, ok := v.counters[key]
	if !ok {
		counter = &Counter{}
		v.counters[key] = counter
		v.seen = append(v.seen, append([]string(nil), values...))
	}
	return counter
}

// snapshot returns every observed label combination and its counter, in
// first-seen order, so rendering output is stable across calls.
func (v *CounterVec) snapshot() []labeledValue {
	v.mu.Lock()
	defer v.mu.Unlock()
	values := make([]labeledValue, 0, len(v.seen))
	for _, labels := range v.seen {
		values = append(values, labeledValue{labels: labels, value: v.counters[labelKey(labels)].Value()})
	}
	return values
}
