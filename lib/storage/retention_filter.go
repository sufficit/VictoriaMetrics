package storage

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/VictoriaMetrics/metricsql"
)

// RetentionFilterSpec associates a Prometheus-style label selector with a retention duration.
//
// Metrics matching Selector are retained for RetentionMs milliseconds instead of
// the global -retentionPeriod. If a metric matches multiple filters, the first match wins.
type RetentionFilterSpec struct {
	// Selector is a Prometheus-style label selector, e.g. {__name__=~"ivr_.+"}
	Selector string

	// RetentionMs is the retention period in milliseconds for matching metrics.
	RetentionMs int64
}

// retentionFilter is the compiled form of RetentionFilterSpec.
type retentionFilter struct {
	matchers    []simpleMatcher
	retentionMs int64
}

// simpleMatcher matches a single label against a value.
type simpleMatcher struct {
	key        string             // empty string means __name__ (MetricGroup)
	value      string
	isNegative bool
	reMatch    func(s string) bool // non-nil when isRegexp
}

func (m *simpleMatcher) match(val []byte) bool {
	s := string(val)
	var matched bool
	if m.reMatch != nil {
		matched = m.reMatch(s)
	} else {
		matched = s == m.value
	}
	if m.isNegative {
		return !matched
	}
	return matched
}

// matchMetricName reports whether mn satisfies all matchers in rf (AND semantics).
func (rf *retentionFilter) matchMetricName(mn *MetricName) bool {
	for i := range rf.matchers {
		m := &rf.matchers[i]
		var val []byte
		if m.key == "" {
			val = mn.MetricGroup
		} else {
			val = mn.GetTagValue(m.key)
		}
		if !m.match(val) {
			return false
		}
	}
	return true
}

// parseRetentionFilters parses a slice of specs each in the form `{selector}:duration`.
func parseRetentionFilters(specs []string) ([]*retentionFilter, error) {
	filters := make([]*retentionFilter, 0, len(specs))
	for _, spec := range specs {
		rf, err := parseRetentionFilter(spec)
		if err != nil {
			return nil, fmt.Errorf("cannot parse retentionFilter %q: %w", spec, err)
		}
		filters = append(filters, rf)
	}
	return filters, nil
}

// parseRetentionFilter parses a single spec: `{label_filters}:duration`.
// The last colon separates the selector from the duration string.
func parseRetentionFilter(spec string) (*retentionFilter, error) {
	idx := strings.LastIndex(spec, ":")
	if idx < 0 {
		return nil, fmt.Errorf("missing ':' separator between selector and duration")
	}
	selectorStr := strings.TrimSpace(spec[:idx])
	durationStr := strings.TrimSpace(spec[idx+1:])

	retentionMs, err := parseRetentionDurationMs(durationStr)
	if err != nil {
		return nil, fmt.Errorf("cannot parse duration %q: %w", durationStr, err)
	}

	matchers, err := parseLabelSelector(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("cannot parse selector %q: %w", selectorStr, err)
	}

	return &retentionFilter{
		matchers:    matchers,
		retentionMs: retentionMs,
	}, nil
}

// parseLabelSelector parses a Prometheus/MetricsQL-style label selector, e.g. {__name__=~"ivr_.+"}.
func parseLabelSelector(selector string) ([]simpleMatcher, error) {
	expr, err := metricsql.Parse(selector)
	if err != nil {
		return nil, fmt.Errorf("cannot parse MetricsQL selector: %w", err)
	}
	me, ok := expr.(*metricsql.MetricExpr)
	if !ok {
		return nil, fmt.Errorf("expected metric selector, got %T", expr)
	}
	if len(me.LabelFilterss) == 0 {
		return nil, fmt.Errorf("empty label selector")
	}
	// Use the first filter group; OR groups are not supported for retention filters.
	lfs := me.LabelFilterss[0]
	matchers := make([]simpleMatcher, 0, len(lfs))
	for _, lf := range lfs {
		m := simpleMatcher{
			key:        lf.Label,
			value:      lf.Value,
			isNegative: lf.IsNegative,
		}
		if lf.Label == "__name__" {
			m.key = "" // empty key means MetricGroup in MetricName
		}
		if lf.IsRegexp {
			re, err := regexp.Compile("^(?:" + lf.Value + ")$")
			if err != nil {
				return nil, fmt.Errorf("cannot compile regexp %q: %w", lf.Value, err)
			}
			m.reMatch = re.MatchString
		}
		matchers = append(matchers, m)
	}
	return matchers, nil
}

// parseRetentionDurationMs parses duration strings like "30d", "1y", "8760h", "2w" into milliseconds.
func parseRetentionDurationMs(s string) (int64, error) {
	if len(s) == 0 {
		return 0, fmt.Errorf("empty duration")
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	var n float64
	if _, err := fmt.Sscanf(numStr, "%f", &n); err != nil {
		return 0, fmt.Errorf("cannot parse number from %q: %w", numStr, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("duration must be positive; got %g", n)
	}
	const msPerSec = 1000
	const msPerMin = 60 * msPerSec
	const msPerHour = 3600 * msPerSec
	const msPerDay = 24 * msPerHour
	const msPerWeek = 7 * msPerDay
	const msPerYear = 365 * msPerDay
	switch unit {
	case 's':
		return int64(n * msPerSec), nil
	case 'm':
		return int64(n * msPerMin), nil
	case 'h':
		return int64(n * msPerHour), nil
	case 'd':
		return int64(n * msPerDay), nil
	case 'w':
		return int64(n * msPerWeek), nil
	case 'y':
		return int64(n * msPerYear), nil
	default:
		return 0, fmt.Errorf("unknown duration unit %q in %q; supported units: s, m, h, d, w, y", unit, s)
	}
}
