// Package metrics is a small, dependency-free counter/histogram registry
// that renders itself in Prometheus text exposition format.
//
// It exists so business and HTTP instrumentation (request counts, AI token
// cost, notification outcomes, ...) can be recorded with ordinary Inc/Add/
// Observe calls without pulling in the full client_golang dependency tree.
// Pull-based single-value gauges (an outbox subscription's lag, a worker's
// liveness) are a separate, simpler mechanism on [health.Registry] instead,
// since they are naturally computed on demand rather than pushed to.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Registry holds every counter and histogram vector created through it and
// renders them together.
type Registry struct {
	counterVec map[string]*registeredCounterVec
	histoVec   map[string]*registeredHistogramVec
	order      []string
}

type registeredCounterVec struct {
	help       string
	labelNames []string
	vec        *CounterVec
}

type registeredHistogramVec struct {
	help       string
	labelNames []string
	vec        *HistogramVec
}

// NewRegistry creates an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{counterVec: map[string]*registeredCounterVec{}, histoVec: map[string]*registeredHistogramVec{}}
}

// NewCounterVec registers a counter distinguished by labelNames, returning
// the vector callers use to record observations via WithLabelValues.
func (r *Registry) NewCounterVec(name, help string, labelNames ...string) *CounterVec {
	vec := newCounterVec()
	r.counterVec[name] = &registeredCounterVec{help: help, labelNames: labelNames, vec: vec}
	r.order = append(r.order, name)
	return vec
}

// NewHistogramVec registers a histogram distinguished by labelNames, using
// buckets as the upper bound of each bucket.
func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labelNames ...string) *HistogramVec {
	vec := newHistogramVec(buckets)
	r.histoVec[name] = &registeredHistogramVec{help: help, labelNames: labelNames, vec: vec}
	r.order = append(r.order, name)
	return vec
}

// Render writes every registered metric in Prometheus text exposition
// format, in registration order.
func (r *Registry) Render(w io.Writer) error {
	builder := &strings.Builder{}
	for _, name := range r.order {
		if entry, ok := r.counterVec[name]; ok {
			writeCounterVec(builder, name, entry)
		}
		if entry, ok := r.histoVec[name]; ok {
			writeHistogramVec(builder, name, entry)
		}
	}
	_, err := w.Write([]byte(builder.String()))
	return err
}

func writeCounterVec(builder *strings.Builder, name string, entry *registeredCounterVec) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s counter\n", name, entry.help, name)
	for _, value := range entry.vec.snapshot() {
		fmt.Fprintf(builder, "%s%s %s\n", name, labelPairs(entry.labelNames, value.labels), formatFloat(value.value))
	}
}

func writeHistogramVec(builder *strings.Builder, name string, entry *registeredHistogramVec) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s histogram\n", name, entry.help, name)
	for _, value := range entry.vec.snapshot() {
		pairs := labelPairs(entry.labelNames, value.labels)
		for i, bound := range value.buckets {
			bucketLabels := appendLabel(pairs, "le", formatFloat(bound))
			fmt.Fprintf(builder, "%s_bucket%s %d\n", name, bucketLabels, value.counts[i])
		}
		infLabels := appendLabel(pairs, "le", "+Inf")
		fmt.Fprintf(builder, "%s_bucket%s %d\n", name, infLabels, value.count)
		fmt.Fprintf(builder, "%s_sum%s %s\n", name, pairs, formatFloat(value.sum))
		fmt.Fprintf(builder, "%s_count%s %d\n", name, pairs, value.count)
	}
}

// labeledValue pairs one observed label combination with its counter value.
type labeledValue struct {
	labels []string
	value  float64
}

// labelKey builds a stable map key from a label-value tuple.
func labelKey(values []string) string { return strings.Join(values, "\x00") }

// labelPairs renders `{name="value",...}` for a Prometheus sample line, or
// an empty string when there are no labels.
func labelPairs(names, values []string) string {
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, len(names))
	for i, name := range names {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		parts[i] = fmt.Sprintf("%s=%q", name, value)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// appendLabel inserts an additional label into an already-rendered label
// block (used for histogram bucket "le" bounds), keeping keys sorted so
// output is deterministic.
func appendLabel(existing, name, value string) string {
	if existing == "" {
		return fmt.Sprintf("{%s=%q}", name, value)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(existing, "{"), "}")
	parts := append(strings.Split(inner, ","), fmt.Sprintf("%s=%q", name, value))
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}

func formatFloat(value float64) string {
	return fmt.Sprintf("%g", value)
}
