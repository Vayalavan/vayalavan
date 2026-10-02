package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler()(rec, httptest.NewRequest(http.MethodGet, MetricsPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape returned %d", rec.Code)
	}
	return rec.Body.String()
}

func TestMetricsCountsByRouteAndStatus(t *testing.T) {
	m := NewMetrics("vm-test-api")
	m.Observe("/orders/{id}", 200, 10*time.Millisecond)
	m.Observe("/orders/{id}", 200, 30*time.Millisecond)
	m.Observe("/orders/{id}", 404, 5*time.Millisecond)

	body := scrape(t, m)

	for _, want := range []string{
		`vayal_requests_total{service="vm-test-api",route="/orders/{id}",status="200"} 2`,
		`vayal_requests_total{service="vm-test-api",route="/orders/{id}",status="404"} 1`,
		`vayal_request_duration_ms_sum{service="vm-test-api",route="/orders/{id}"} 45`,
		`vayal_request_duration_ms_count{service="vm-test-api",route="/orders/{id}"} 3`,
		`vayal_request_duration_ms_max{service="vm-test-api",route="/orders/{id}"} 30`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\ngot:\n%s", want, body)
		}
	}
}

// TestMetricsSeparatesErrorClasses — a 5xx is what wakes someone up, and it
// must not be buried among 4xx noise from ordinary validation failures.
func TestMetricsSeparatesErrorClasses(t *testing.T) {
	m := NewMetrics("vm-test-api")
	m.Observe("/a", 200, time.Millisecond)
	m.Observe("/a", 422, time.Millisecond)
	m.Observe("/a", 404, time.Millisecond)
	m.Observe("/b", 500, time.Millisecond)
	m.Observe("/b", 503, time.Millisecond)

	body := scrape(t, m)
	if !strings.Contains(body, `vayal_errors_total{service="vm-test-api",class="server"} 2`) {
		t.Errorf("server errors miscounted:\n%s", body)
	}
	if !strings.Contains(body, `vayal_errors_total{service="vm-test-api",class="client"} 2`) {
		t.Errorf("client errors miscounted:\n%s", body)
	}
}

// TestUnmatchedRoutesShareOneLabel is the cardinality guard: an attacker
// probing thousands of random URLs must not create thousands of label values
// and take the metrics store down with them.
func TestUnmatchedRoutesShareOneLabel(t *testing.T) {
	m := NewMetrics("vm-test-api")
	for _, path := range []string{"", "", ""} {
		m.Observe(path, 404, time.Millisecond)
	}
	body := scrape(t, m)
	if !strings.Contains(body, `route="unmatched",status="404"} 3`) {
		t.Errorf("unmatched requests were not collapsed:\n%s", body)
	}
}

func TestLabelValuesAreEscaped(t *testing.T) {
	m := NewMetrics("vm-test-api")
	// A quote in a label would otherwise produce a malformed scrape that
	// breaks the whole endpoint, not just this one series.
	m.Observe(`/evil"route`, 200, time.Millisecond)
	body := scrape(t, m)
	if !strings.Contains(body, `route="/evil\"route"`) {
		t.Errorf("label not escaped:\n%s", body)
	}
}

func TestInFlightReturnsToZero(t *testing.T) {
	m := NewMetrics("vm-test-api")
	done := m.TrackInFlight()
	if got := m.inFlight.Load(); got != 1 {
		t.Fatalf("in flight = %d, want 1", got)
	}
	done()
	if got := m.inFlight.Load(); got != 0 {
		t.Errorf("in flight = %d after completion, want 0", got)
	}
}

// TestConcurrentObserveIsRaceFree runs under -race in CI. Metrics are written
// from every request goroutine at once; losing counts or corrupting the maps
// would be a bug that only appears under load, which is exactly when the
// numbers matter.
func TestConcurrentObserveIsRaceFree(t *testing.T) {
	m := NewMetrics("vm-test-api")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				m.Observe("/catalog", 200, time.Duration(j)*time.Millisecond)
			}
		}()
	}
	wg.Wait()

	if !strings.Contains(scrape(t, m),
		`vayal_request_duration_ms_count{service="vm-test-api",route="/catalog"} 1000`) {
		t.Errorf("lost counts under concurrency:\n%s", scrape(t, m))
	}
}
