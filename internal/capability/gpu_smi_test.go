package capability

import (
	"math"
	"testing"
)

func TestParseMetricValueUnavailable(t *testing.T) {
	for _, value := range []string{"[N/A]", "[Not Supported]", " [n/a] "} {
		got, err := parseMetricValue(value)
		if err != nil {
			t.Fatalf("parseMetricValue(%q) returned error: %v", value, err)
		}
		if got != 0 {
			t.Errorf("parseMetricValue(%q) = %v, want 0", value, got)
		}
	}
}

func TestParseMetricValueNumbers(t *testing.T) {
	for value, want := range map[string]float64{
		"1432":   1432,
		"12.5":   12.5,
		" 41.25 ": 41.25,
	} {
		got, err := parseMetricValue(value)
		if err != nil {
			t.Fatalf("parseMetricValue(%q) returned error: %v", value, err)
		}
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("parseMetricValue(%q) = %v, want %v", value, got, want)
		}
	}
}
