package storage

import (
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fs"
)

// TestRetentionFilterDurationParsing tests parseRetentionDurationMs with all supported units,
// including the new "ms" suffix.
func TestRetentionFilterDurationParsing(t *testing.T) {
	f := func(s string, wantMs int64) {
		t.Helper()
		got, err := parseRetentionDurationMs(s)
		if err != nil {
			t.Fatalf("unexpected error for %q: %s", s, err)
		}
		if got != wantMs {
			t.Fatalf("unexpected result for %q: got %d, want %d", s, got, wantMs)
		}
	}

	// Milliseconds (new unit)
	f("1ms", 1)
	f("100ms", 100)
	f("500ms", 500)
	f("1000ms", 1000)

	// Seconds
	f("1s", 1_000)
	f("30s", 30_000)

	// Minutes
	f("1m", 60_000)
	f("5m", 300_000)

	// Hours
	f("1h", 3_600_000)

	// Days
	f("1d", 86_400_000)

	// Weeks
	f("1w", 7*86_400_000)

	// Years
	f("1y", 365*86_400_000)

	// 1000ms must equal 1s
	ms, _ := parseRetentionDurationMs("1000ms")
	sec, _ := parseRetentionDurationMs("1s")
	if ms != sec {
		t.Fatalf("1000ms (%d) != 1s (%d)", ms, sec)
	}
}

// TestRetentionFilterDurationParsingErrors checks that invalid inputs are rejected.
func TestRetentionFilterDurationParsingErrors(t *testing.T) {
	bad := func(s string) {
		t.Helper()
		if _, err := parseRetentionDurationMs(s); err == nil {
			t.Fatalf("expected error for %q, got nil", s)
		}
	}

	bad("")
	bad("0ms")
	bad("-1ms")
	bad("0s")
	bad("abc")
	bad("1x")
	bad("ms")
}

// TestRetentionFilter_OldDataIsRemoved inserts a metric whose only data point is
// older than the per-filter retention window and verifies it is dropped after a
// forced partition merge.
//
// The test relies on timestamps rather than real clock sleep: rows are inserted
// with a timestamp that is already outside the 500ms retention window, so
// ForceMergePartitions removes them immediately without any actual waiting.
// Storage is closed and reopened before the merge to ensure all index entries
// are fully flushed and searchable.
func TestRetentionFilter_OldDataIsRemoved(t *testing.T) {
	path := "TestRetentionFilter_OldDataIsRemoved"
	const filterRetentionMs = 500 // 500 ms

	opts := OpenOptions{
		// Global retention: very long so it does not interfere with the per-filter check.
		Retention: 10 * retention31Days,
		// Only "short_metric" uses the 500 ms window.
		RetentionFilters: []string{`{__name__="short_metric"}:500ms`},
	}

	// Phase 1: insert and persist.
	func() {
		s := MustOpenStorage(path, opts)
		defer s.MustClose()

		// Row timestamp: 2 seconds ago, well beyond the 500 ms filter retention.
		oldTimestamp := time.Now().Add(-2 * time.Second).UnixMilli()
		var mn MetricName
		mn.MetricGroup = []byte("short_metric")
		s.AddRows([]MetricRow{{
			MetricNameRaw: mn.marshalRaw(nil),
			Timestamp:     oldTimestamp,
			Value:         42.0,
		}}, defaultPrecisionBits)
		s.DebugFlush()
	}()

	// Phase 2: reopen, verify rows exist, merge, verify rows gone.
	s := MustOpenStorage(path, opts)
	defer func() {
		s.MustClose()
		fs.MustRemoveDir(path)
	}()

	var m1 Metrics
	s.UpdateMetrics(&m1)
	if m1.TableMetrics.TotalRowsCount() == 0 {
		t.Fatal("expected at least one row after reopen")
	}

	// Force a full compaction; the retention filter must drop the expired row.
	if err := s.ForceMergePartitions(""); err != nil {
		t.Fatalf("ForceMergePartitions error: %s", err)
	}

	var m2 Metrics
	s.UpdateMetrics(&m2)
	if got := m2.TableMetrics.TotalRowsCount(); got != 0 {
		t.Fatalf("expected 0 rows after ForceMergePartitions, got %d (row should have been removed by %dms retention filter)", got, filterRetentionMs)
	}
}

// TestRetentionFilter_RecentDataIsRetained inserts a metric with a current timestamp
// and verifies it survives the forced partition merge, because it is still within
// the per-filter retention window.
func TestRetentionFilter_RecentDataIsRetained(t *testing.T) {
	path := "TestRetentionFilter_RecentDataIsRetained"

	opts := OpenOptions{
		Retention:        10 * retention31Days,
		RetentionFilters: []string{`{__name__="short_metric"}:500ms`},
	}

	// Phase 1: insert and persist.
	func() {
		s := MustOpenStorage(path, opts)
		defer s.MustClose()

		var mn MetricName
		mn.MetricGroup = []byte("short_metric")
		s.AddRows([]MetricRow{{
			MetricNameRaw: mn.marshalRaw(nil),
			Timestamp:     time.Now().UnixMilli(),
			Value:         1.0,
		}}, defaultPrecisionBits)
		s.DebugFlush()
	}()

	// Phase 2: reopen and merge.
	s := MustOpenStorage(path, opts)
	defer func() {
		s.MustClose()
		fs.MustRemoveDir(path)
	}()

	if err := s.ForceMergePartitions(""); err != nil {
		t.Fatalf("ForceMergePartitions error: %s", err)
	}

	var m Metrics
	s.UpdateMetrics(&m)
	if got := m.TableMetrics.TotalRowsCount(); got == 0 {
		t.Fatal("expected row to survive ForceMergePartitions (within 500ms retention window)")
	}
}

// TestRetentionFilter_NonMatchingMetricIsUnaffected inserts two metrics: one
// matching the short retention filter and one that does not. After a forced merge
// the non-matching metric must survive regardless of its age, because only the
// global (long) retention applies to it.
func TestRetentionFilter_NonMatchingMetricIsUnaffected(t *testing.T) {
	path := "TestRetentionFilter_NonMatchingMetricIsUnaffected"

	opts := OpenOptions{
		Retention:        10 * retention31Days,
		RetentionFilters: []string{`{__name__="short_metric"}:500ms`},
	}

	// Phase 1: insert and persist.
	func() {
		s := MustOpenStorage(path, opts)
		defer s.MustClose()

		// Both rows are 2 seconds old: short_metric is outside its 500ms window,
		// but long_metric is well within the global 10-month window.
		oldTimestamp := time.Now().Add(-2 * time.Second).UnixMilli()
		var mnShort MetricName
		mnShort.MetricGroup = []byte("short_metric")
		var mnLong MetricName
		mnLong.MetricGroup = []byte("long_metric")

		s.AddRows([]MetricRow{
			{MetricNameRaw: mnShort.marshalRaw(nil), Timestamp: oldTimestamp, Value: 1.0},
			{MetricNameRaw: mnLong.marshalRaw(nil), Timestamp: oldTimestamp, Value: 2.0},
		}, defaultPrecisionBits)
		s.DebugFlush()
	}()

	// Phase 2: reopen and merge.
	s := MustOpenStorage(path, opts)
	defer func() {
		s.MustClose()
		fs.MustRemoveDir(path)
	}()

	if err := s.ForceMergePartitions(""); err != nil {
		t.Fatalf("ForceMergePartitions error: %s", err)
	}

	var m Metrics
	s.UpdateMetrics(&m)
	got := m.TableMetrics.TotalRowsCount()
	// Exactly 1 row must remain: the long_metric one.
	if got != 1 {
		t.Fatalf("expected exactly 1 row after merge (long_metric must survive), got %d", got)
	}
}
