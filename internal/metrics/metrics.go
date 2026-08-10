// Package metrics exposes counters and gauges in Prometheus text format.
//
// Hand-rolled deliberately. The project adds no third-party dependency, and the
// exposition format is a handful of lines per metric — importing
// prometheus/client_golang to emit them would pull in protobuf, procfs and
// common for something this file does in 120 lines.
//
// Registration is process-global and happens at package-variable initialisation
// in the packages that own each metric. That mirrors how the standard library
// treats expvar, and it means a metric cannot be forgotten at wiring time: if
// the var exists, it is exposed.
package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type kind string

const (
	kindCounter kind = "counter"
	kindGauge   kind = "gauge"
)

type entry struct {
	name  string
	help  string
	kind  kind
	label string // empty for unlabelled metrics

	simple *Count            // unlabelled counter
	level  *Level            // gauge
	vecMu  sync.RWMutex      // guards vec
	vec    map[string]*Count // labelled counter, keyed by label value
}

var (
	registryMu sync.RWMutex
	registry   []*entry
)

func register(e *entry) *entry {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, e)
	return e
}

// Count is a monotonically increasing counter.
type Count struct{ n atomic.Int64 }

func (c *Count) Inc()         { c.n.Add(1) }
func (c *Count) Add(n int64)  { c.n.Add(n) }
func (c *Count) Value() int64 { return c.n.Load() }

// Level is a gauge: a value that goes up and down.
//
// Stored as the IEEE-754 bits in an atomic uint64 rather than behind a mutex,
// because gauges are set from the ingest goroutine while the scrape goroutine
// reads them, and a torn float64 read on a 32-bit platform is a real (if rare)
// possibility that math.Float64bits + atomic removes for free.
type Level struct{ bits atomic.Uint64 }

func (g *Level) Set(v float64)  { g.bits.Store(float64bits(v)) }
func (g *Level) Value() float64 { return float64frombits(g.bits.Load()) }

// Vec is a counter with one label dimension.
type Vec struct{ e *entry }

// With returns the counter for one label value, creating it on first use.
//
// The caller MUST pass a bounded set of values. Label cardinality is the
// classic way a metrics endpoint becomes a memory leak: labelling by raw
// request path or by client IP grows the map without limit, and an attacker can
// then exhaust memory by varying the label. Route labels must be the route
// PATTERN ("/api/v1/area/{slug}/sensors"), never the concrete path.
func (v *Vec) With(value string) *Count {
	v.e.vecMu.RLock()
	c, ok := v.e.vec[value]
	v.e.vecMu.RUnlock()
	if ok {
		return c
	}

	v.e.vecMu.Lock()
	defer v.e.vecMu.Unlock()
	if c, ok := v.e.vec[value]; ok {
		return c
	}
	c = &Count{}
	v.e.vec[value] = c
	return c
}

func Counter(name, help string) *Count {
	c := &Count{}
	register(&entry{name: name, help: help, kind: kindCounter, simple: c})
	return c
}

func CounterVec(name, help, label string) *Vec {
	e := register(&entry{
		name: name, help: help, kind: kindCounter, label: label,
		vec: map[string]*Count{},
	})
	return &Vec{e: e}
}

func Gauge(name, help string) *Level {
	g := &Level{}
	register(&entry{name: name, help: help, kind: kindGauge, level: g})
	return g
}

// Handler serves the exposition. It must be mounted on the PRIVATE listener
// only: metric names and counts describe internal behaviour and request volume,
// which is reconnaissance material for anyone probing the rate limits.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registryMu.RLock()
		snapshot := make([]*entry, len(registry))
		copy(snapshot, registry)
		registryMu.RUnlock()

		var b strings.Builder
		for _, e := range snapshot {
			fmt.Fprintf(&b, "# HELP %s %s\n", e.name, e.help)
			fmt.Fprintf(&b, "# TYPE %s %s\n", e.name, e.kind)

			switch {
			case e.simple != nil:
				fmt.Fprintf(&b, "%s %d\n", e.name, e.simple.Value())
			case e.level != nil:
				fmt.Fprintf(&b, "%s %s\n", e.name,
					strconv.FormatFloat(e.level.Value(), 'g', -1, 64))
			default:
				e.vecMu.RLock()
				keys := make([]string, 0, len(e.vec))
				for k := range e.vec {
					keys = append(keys, k)
				}
				// Sorted so successive scrapes produce identical output for
				// identical state. Map order would make the exposition differ
				// run to run, which makes diffing a scrape useless.
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&b, "%s{%s=\"%s\"} %d\n",
						e.name, e.label, escapeLabelValue(k), e.vec[k].Value())
				}
				e.vecMu.RUnlock()
			}
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
}

// escapeLabelValue escapes the three characters the exposition format reserves.
// Label values derive from request data, so an unescaped quote or newline lets a
// caller inject synthetic metric lines into the scrape — corrupting the very
// dashboard an operator would use to notice the abuse.
func escapeLabelValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Thin wrappers so the atomic gauge reads clearly at the call site.
func float64bits(f float64) uint64     { return math.Float64bits(f) }
func float64frombits(b uint64) float64 { return math.Float64frombits(b) }

// Vec carries exactly one label dimension (Task 5), so route and status are
// two vectors rather than one two-label vector. That is not a workaround: the
// cross product of route x status is the cardinality that would need bounding,
// and keeping them separate makes the bound structural.
var (
	httpRequests  = CounterVec("airbg_http_requests_total", "HTTP requests served, by route.", "route")
	httpResponses = CounterVec("airbg_http_responses_total", "HTTP responses served, by status.", "status")
)

// Instrument counts requests by ROUTE PATTERN and status.
//
// r.Pattern, never r.URL.Path: the path is attacker-controlled, and one label
// per distinct path turns the metrics registry into an unbounded map that any
// client can grow — the counter that reports the attack becomes the attack.
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			// An unmatched request has no pattern. Labelling it with the path
			// would hand an attacker a way to grow the map at will.
			route = "unmatched"
		}
		httpRequests.With(route).Inc()
		httpResponses.With(strconv.Itoa(rec.status)).Inc()
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
