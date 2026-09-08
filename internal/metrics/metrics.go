// Package metrics is a tiny dependency-free Prometheus text exposition helper.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Counter is a monotonically increasing float counter.
type Counter struct{ v atomic.Int64 }

func (c *Counter) Add(n int64) { c.v.Add(n) }
func (c *Counter) Inc()        { c.v.Add(1) }
func (c *Counter) Get() int64  { return c.v.Load() }

// LabeledCounter keys counters by a label tuple rendered as `a="x",b="y"`.
type LabeledCounter struct {
	mu sync.RWMutex
	m  map[string]*Counter
}

func NewLabeledCounter() *LabeledCounter { return &LabeledCounter{m: map[string]*Counter{}} }

func (l *LabeledCounter) With(labels string) *Counter {
	l.mu.RLock()
	c, ok := l.m[labels]
	l.mu.RUnlock()
	if ok {
		return c
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok = l.m[labels]; !ok {
		c = &Counter{}
		l.m[labels] = c
	}
	return c
}

func (l *LabeledCounter) each(fn func(labels string, v int64)) {
	l.mu.RLock()
	keys := make([]string, 0, len(l.m))
	for k := range l.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fn(k, l.m[k].Get())
	}
	l.mu.RUnlock()
}

// Histogram is a fixed-bucket cumulative histogram (seconds).
type Histogram struct {
	bounds []float64
	counts []atomic.Int64
	sum    atomic.Int64 // microseconds
	total  atomic.Int64
}

func NewHistogram(bounds []float64) *Histogram {
	return &Histogram{bounds: bounds, counts: make([]atomic.Int64, len(bounds))}
}

func (h *Histogram) Observe(seconds float64) {
	for i, b := range h.bounds {
		if seconds <= b {
			h.counts[i].Add(1)
		}
	}
	h.total.Add(1)
	h.sum.Add(int64(seconds * 1e6))
}

// Writer renders metrics in Prometheus text format.
type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (p *Writer) Header(name, help, typ string) {
	fmt.Fprintf(p.w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

func (p *Writer) Gauge(name, help string, v float64) {
	p.Header(name, help, "gauge")
	fmt.Fprintf(p.w, "%s %s\n", name, fmtFloat(v))
}

func (p *Writer) GaugeLabeled(name, help string, rows [][2]any) {
	p.Header(name, help, "gauge")
	for _, r := range rows {
		fmt.Fprintf(p.w, "%s{%s} %s\n", name, r[0], fmtFloat(toFloat(r[1])))
	}
}

func (p *Writer) Counter(name, help string, c *Counter) {
	p.Header(name, help, "counter")
	fmt.Fprintf(p.w, "%s %d\n", name, c.Get())
}

func (p *Writer) LabeledCounter(name, help string, lc *LabeledCounter) {
	p.Header(name, help, "counter")
	lc.each(func(labels string, v int64) { fmt.Fprintf(p.w, "%s{%s} %d\n", name, labels, v) })
}

func (p *Writer) Histogram(name, help string, h *Histogram) {
	p.Header(name, help, "histogram")
	for i, b := range h.bounds {
		fmt.Fprintf(p.w, "%s_bucket{le=\"%s\"} %d\n", name, fmtFloat(b), h.counts[i].Load())
	}
	fmt.Fprintf(p.w, "%s_bucket{le=\"+Inf\"} %d\n", name, h.total.Load())
	fmt.Fprintf(p.w, "%s_sum %s\n", name, fmtFloat(float64(h.sum.Load())/1e6))
	fmt.Fprintf(p.w, "%s_count %d\n", name, h.total.Load())
}

// Label escapes a label value.
func Label(k, v string) string {
	v = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
	return k + `="` + v + `"`
}

func fmtFloat(f float64) string {
	s := fmt.Sprintf("%g", f)
	return s
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	}
	return 0
}
