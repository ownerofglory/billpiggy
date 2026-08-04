package metrics

import "sync"

// DefaultLatencyBuckets are upper bounds in seconds, suited to HTTP request
// latency.
var DefaultLatencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Histogram accumulates observations into fixed, ascending buckets alongside
// a running sum and count, matching Prometheus's cumulative-bucket model.
type Histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

func newHistogram(buckets []float64) *Histogram {
	return &Histogram{buckets: buckets, counts: make([]uint64, len(buckets))}
}

// Observe records one value, incrementing every bucket whose upper bound is
// at or above it.
func (h *Histogram) Observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, bound := range h.buckets {
		if value <= bound {
			h.counts[i]++
		}
	}
	h.sum += value
	h.count++
}

type histogramSnapshot struct {
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

func (h *Histogram) snapshot() histogramSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return histogramSnapshot{buckets: h.buckets, counts: append([]uint64(nil), h.counts...), sum: h.sum, count: h.count}
}

// HistogramVec is a set of histograms distinguished by a fixed list of label values.
type HistogramVec struct {
	mu         sync.Mutex
	buckets    []float64
	histograms map[string]*Histogram
	seen       [][]string
}

func newHistogramVec(buckets []float64) *HistogramVec {
	return &HistogramVec{buckets: buckets, histograms: map[string]*Histogram{}}
}

// WithLabelValues returns the histogram for this exact combination of label
// values, creating it on first use.
func (v *HistogramVec) WithLabelValues(values ...string) *Histogram {
	key := labelKey(values)
	v.mu.Lock()
	defer v.mu.Unlock()
	histogram, ok := v.histograms[key]
	if !ok {
		histogram = newHistogram(v.buckets)
		v.histograms[key] = histogram
		v.seen = append(v.seen, append([]string(nil), values...))
	}
	return histogram
}

type labeledHistogram struct {
	labels []string
	histogramSnapshot
}

func (v *HistogramVec) snapshot() []labeledHistogram {
	v.mu.Lock()
	defer v.mu.Unlock()
	values := make([]labeledHistogram, 0, len(v.seen))
	for _, labels := range v.seen {
		values = append(values, labeledHistogram{labels: labels, histogramSnapshot: v.histograms[labelKey(labels)].snapshot()})
	}
	return values
}
