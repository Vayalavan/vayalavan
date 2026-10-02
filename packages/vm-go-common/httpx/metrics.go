package httpx

import (
	"expvar"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsPath is where every service exposes its counters.
const MetricsPath = "/metrics"

// Metrics is a small in-process counter set, exposed as Prometheus text.
//
// Hand-rolled rather than pulling in the Prometheus client library. What is
// needed at launch is the ability to answer three questions during an
// incident — is it erroring, is it slow, and which route — and that is a few
// counters. A scrape endpoint in the standard text format means swapping in
// the real client later changes this file and nothing else.
//
// Deliberately NOT per-path-with-ids: a label per order id would produce
// unbounded cardinality and take the metrics store down with it. Routes are
// recorded as chi patterns ("/orders/{id}"), which is a fixed small set.
type Metrics struct {
	service string
	started time.Time

	requests  sync.Map // routeStatus -> *atomic.Int64
	durations sync.Map // route -> *durationStat

	inFlight atomic.Int64
	// Counted separately because a 5xx is the signal that wakes someone up,
	// and summing it out of the per-route map during an incident is friction.
	serverErrors atomic.Int64
	clientErrors atomic.Int64
}

type routeStatus struct {
	route  string
	status int
}

// durationStat is a running total and count, enough for an average. Not a
// histogram: percentiles need bucket choices that should be made against real
// traffic, and a wrong bucket layout is worse than an honest mean.
type durationStat struct {
	totalMS atomic.Int64
	count   atomic.Int64
	maxMS   atomic.Int64
}

// NewMetrics creates a counter set for one service.
func NewMetrics(service string) *Metrics {
	return &Metrics{service: service, started: time.Now()}
}

// Observe records one completed request.
func (m *Metrics) Observe(route string, status int, duration time.Duration) {
	if route == "" {
		// An unmatched request has no pattern. Bucketed under a constant so an
		// attacker probing random URLs cannot inflate cardinality.
		route = "unmatched"
	}

	key := routeStatus{route: route, status: status}
	counter, _ := m.requests.LoadOrStore(key, new(atomic.Int64))
	counter.(*atomic.Int64).Add(1)

	stat, _ := m.durations.LoadOrStore(route, new(durationStat))
	ds := stat.(*durationStat)
	ms := duration.Milliseconds()
	ds.totalMS.Add(ms)
	ds.count.Add(1)
	// Compare-and-swap loop: two requests finishing together must not lose the
	// larger value.
	for {
		current := ds.maxMS.Load()
		if ms <= current || ds.maxMS.CompareAndSwap(current, ms) {
			break
		}
	}

	switch {
	case status >= 500:
		m.serverErrors.Add(1)
	case status >= 400:
		m.clientErrors.Add(1)
	}
}

// TrackInFlight brackets a request. The returned func must be deferred.
func (m *Metrics) TrackInFlight() func() {
	m.inFlight.Add(1)
	return func() { m.inFlight.Add(-1) }
}

// Handler serves the counters in Prometheus text exposition format.
//
// Not exposed publicly — the gateway does not route to it (CLAUDE.md rule 5),
// so it is reachable only from inside the network, like the services
// themselves.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		// Never cached: a scraped metric served from a cache is a lie about
		// the present.
		w.Header().Set("Cache-Control", "no-store")

		write := func(s string) { _, _ = w.Write([]byte(s)) }
		label := `service="` + m.service + `"`

		write("# HELP vayal_uptime_seconds Seconds since this process started.\n")
		write("# TYPE vayal_uptime_seconds gauge\n")
		write("vayal_uptime_seconds{" + label + "} " +
			strconv.FormatInt(int64(time.Since(m.started).Seconds()), 10) + "\n")

		write("# HELP vayal_requests_in_flight Requests currently being served.\n")
		write("# TYPE vayal_requests_in_flight gauge\n")
		write("vayal_requests_in_flight{" + label + "} " +
			strconv.FormatInt(m.inFlight.Load(), 10) + "\n")

		write("# HELP vayal_errors_total Responses by error class.\n")
		write("# TYPE vayal_errors_total counter\n")
		write("vayal_errors_total{" + label + `,class="server"} ` +
			strconv.FormatInt(m.serverErrors.Load(), 10) + "\n")
		write("vayal_errors_total{" + label + `,class="client"} ` +
			strconv.FormatInt(m.clientErrors.Load(), 10) + "\n")

		write("# HELP vayal_requests_total Completed requests by route and status.\n")
		write("# TYPE vayal_requests_total counter\n")
		m.requests.Range(func(key, value any) bool {
			ks := key.(routeStatus)
			write("vayal_requests_total{" + label +
				`,route="` + escapeLabel(ks.route) + `"` +
				`,status="` + strconv.Itoa(ks.status) + `"} ` +
				strconv.FormatInt(value.(*atomic.Int64).Load(), 10) + "\n")
			return true
		})

		write("# HELP vayal_request_duration_ms_sum Total handling time per route.\n")
		write("# TYPE vayal_request_duration_ms_sum counter\n")
		write("# HELP vayal_request_duration_ms_count Requests measured per route.\n")
		write("# TYPE vayal_request_duration_ms_count counter\n")
		write("# HELP vayal_request_duration_ms_max Slowest request seen per route.\n")
		write("# TYPE vayal_request_duration_ms_max gauge\n")
		m.durations.Range(func(key, value any) bool {
			route := escapeLabel(key.(string))
			ds := value.(*durationStat)
			write("vayal_request_duration_ms_sum{" + label + `,route="` + route + `"} ` +
				strconv.FormatInt(ds.totalMS.Load(), 10) + "\n")
			write("vayal_request_duration_ms_count{" + label + `,route="` + route + `"} ` +
				strconv.FormatInt(ds.count.Load(), 10) + "\n")
			write("vayal_request_duration_ms_max{" + label + `,route="` + route + `"} ` +
				strconv.FormatInt(ds.maxMS.Load(), 10) + "\n")
			return true
		})

		// Go runtime counters, which answer "is it the process or the query?"
		expvar.Do(func(kv expvar.KeyValue) {
			if kv.Key == "memstats" {
				return // too large and too noisy for a scrape
			}
		})
	}
}

// escapeLabel makes a route safe inside a Prometheus label value.
func escapeLabel(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '"':
			out = append(out, '\\', s[i])
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
