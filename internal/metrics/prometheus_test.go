package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestNewPrometheusServiceStarted(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewPrometheus(reg, "bot@example.com", "2026.3.1")

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var found bool
	for _, mf := range mfs {
		if mf.GetName() != namespace+"_service_started" {
			continue
		}
		found = true
		if len(mf.GetMetric()) != 1 {
			t.Fatalf("metric count = %d, want 1", len(mf.GetMetric()))
		}
		m := mf.GetMetric()[0]
		if got := m.GetCounter().GetValue(); got != 1 {
			t.Fatalf("value = %v, want 1", got)
		}
		labels := labelMap(m)
		if labels["bot_id"] != "bot@example.com" {
			t.Fatalf("bot_id = %q", labels["bot_id"])
		}
		if labels["version"] != "2026.3.1" {
			t.Fatalf("version = %q", labels["version"])
		}
	}
	if !found {
		names := make([]string, 0, len(mfs))
		for _, mf := range mfs {
			names = append(names, mf.GetName())
		}
		t.Fatalf("service_started not found; metrics: %s", strings.Join(names, ", "))
	}
}

func labelMap(m *dto.Metric) map[string]string {
	out := make(map[string]string, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		out[lp.GetName()] = lp.GetValue()
	}
	return out
}
