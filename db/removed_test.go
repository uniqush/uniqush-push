package db

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestRemovedLogReportsTruncation checks that a cursor is reported truncated
// exactly when removals after it have been trimmed away.
func TestRemovedLogReportsTruncation(t *testing.T) {
	r := connectDatabaseAndClearRedisData(t).(*pushDatabaseOpts).db.(*PushRedisDB)
	r.removedLogLength = 3

	record := func(i int) {
		t.Helper()
		err := r.RecordRemovedDeliveryPoint(RemovedDeliveryPoint{Service: ServiceName, Subscriber: strconv.Itoa(i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	record(1)
	record(2)
	early, truncated, err := r.RemovedDeliveryPoints(ServiceName, "0", 10)
	if err != nil || truncated || len(early) != 2 {
		t.Fatalf("Expected two removals and nothing trimmed, got %v, %v, %v", early, truncated, err)
	}
	for i := 3; i <= 5; i++ {
		record(i) // trims 1 and 2
	}

	latest, _, err := r.RemovedDeliveryPoints(ServiceName, "0", 10)
	if err != nil || len(latest) != 3 {
		t.Fatalf("Expected the 3 kept removals, got %v, %v", latest, err)
	}
	newest := latest[2].ID
	newestMS, _, _ := strings.Cut(newest, "-")

	cases := []struct {
		name          string
		after         string
		wantFirst     int // subscriber of the first entry returned, 0 for none
		wantTruncated bool
		wantErr       bool
	}{
		{"from the start, after a trim", "0", 3, true, false},
		{"cursor at a trimmed entry with a later one trimmed", early[0].ID, 3, true, false},
		{"cursor at the last trimmed entry", early[1].ID, 3, false, false},
		{"cursor at the newest entry", newest, 0, false, false},
		{"cursor past the newest entry", newestMS + "-999", 0, false, true},
		{"cursor in the future", "99999999999999-0", 0, false, true},
		{"malformed cursor", "not-a-cursor", 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, truncated, err := r.RemovedDeliveryPoints(ServiceName, c.after, 10)
			if c.wantErr {
				if !errors.Is(err, ErrInvalidCursor) {
					t.Errorf("Expected ErrInvalidCursor, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			first := 0
			if len(got) > 0 {
				first, _ = strconv.Atoi(got[0].Subscriber)
			}
			if first != c.wantFirst || truncated != c.wantTruncated {
				t.Errorf("Expected entries from %d and truncated=%v, got %+v, truncated=%v",
					c.wantFirst, c.wantTruncated, got, truncated)
			}
		})
	}

	if _, _, err := r.RemovedDeliveryPoints(OtherServiceName, "1-0", 10); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("Expected a cursor into a service with no removals to be refused, got %v", err)
	}
}
