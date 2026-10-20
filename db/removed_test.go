package db

import (
	"strconv"
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

	cases := []struct {
		name          string
		after         string
		wantTruncated bool
	}{
		{"from the start, after a trim", "0", true},
		{"cursor at a trimmed entry with a later one trimmed", early[0].ID, true},
		{"cursor at the last trimmed entry", early[1].ID, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, truncated, err := r.RemovedDeliveryPoints(ServiceName, c.after, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 || got[0].Subscriber != "3" || truncated != c.wantTruncated {
				t.Errorf("Expected the 3 kept removals and truncated=%v, got %+v, truncated=%v",
					c.wantTruncated, got, truncated)
			}
		})
	}

	latest, _, _ := r.RemovedDeliveryPoints(ServiceName, "0", 10)
	if got, truncated, err := r.RemovedDeliveryPoints(ServiceName, latest[2].ID, 10); err != nil || truncated || len(got) != 0 {
		t.Errorf("Expected nothing after the newest removal, got %v, %v, %v", got, truncated, err)
	}
	if _, _, err := r.RemovedDeliveryPoints(ServiceName, "not-a-cursor", 10); err == nil {
		t.Error("Expected an invalid cursor to be refused")
	}
}
