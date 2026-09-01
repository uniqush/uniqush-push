package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// findProblems returns the reported problems of one kind.
func findProblems(report *ConsistencyReport, kind string) []ConsistencyProblem {
	var found []ConsistencyProblem
	for _, problem := range report.Problems {
		if problem.Kind == kind {
			found = append(found, problem)
		}
	}
	return found
}

func checkConsistency(t *testing.T, fixture *rebindingFixture) *ConsistencyReport {
	t.Helper()
	report, err := fixture.client.CheckConsistency()
	if err != nil {
		t.Fatalf("CheckConsistency failed: %v", err)
	}
	return report
}

// TestConsistencyCheckIsQuietOnAHealthyDatabase is the test that gives the
// others meaning.
//
// A check that reports something about every database says nothing about any of
// them, and would train an operator to ignore it.
func TestConsistencyCheckIsQuietOnAHealthyDatabase(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")
	fixture.subscribe(t, "devtoken-2")

	report := checkConsistency(t, fixture)
	if !report.Healthy() {
		t.Errorf("Expected no problems on a freshly built database, got: %v", report.Problems)
	}
	if report.Providers != 1 {
		t.Errorf("Expected 1 provider, got %d", report.Providers)
	}
	if report.DeliveryPoints != 2 {
		t.Errorf("Expected 2 delivery points, got %d", report.DeliveryPoints)
	}
	if !strings.Contains(report.Summary(), "no problems found") {
		t.Errorf("Expected a clean summary, got %q", report.Summary())
	}
}

// TestConsistencyCheckFindsDuplicateProviders covers the one finding that is a
// correctness problem rather than debris.
//
// Only a database written before PR #201 can be in this state, and it is
// exactly the state where deriving a provider is ambiguous.
func TestConsistencyCheckFindsDuplicateProviders(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// Written behind the conflict check, as a pre-PR-#201 database would have.
	second, err := fixture.psm.BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         ServiceName,
		"cert":            "second.cert",
		"key":             "second.cert.key",
	})
	if err != nil {
		t.Fatalf("Could not build a second provider: %v", err)
	}
	if err := fixture.raw.SetPushServiceProvider(second); err != nil {
		t.Fatalf("Could not write the second provider: %v", err)
	}
	if err := fixture.raw.AddPushServiceProviderToService(ServiceName, second.Name()); err != nil {
		t.Fatalf("Could not add the second provider: %v", err)
	}

	report := checkConsistency(t, fixture)
	duplicates := findProblems(report, ProblemDuplicateProvider)
	if len(duplicates) != 1 {
		t.Fatalf("Expected one duplicate-provider problem, got %d: %v", len(duplicates), report.Problems)
	}
	if duplicates[0].Service != ServiceName {
		t.Errorf("Expected the problem to name service %q, got %q", ServiceName, duplicates[0].Service)
	}
	// The detail has to say what to do about it, or the report is just an alarm.
	if !strings.Contains(duplicates[0].Detail, "/rmpsp") {
		t.Errorf("Expected the detail to say how to fix it, got %q", duplicates[0].Detail)
	}
}

// TestConsistencyCheckFindsDanglingProviders covers an interrupted write:
// a name in srv-2-psp with no record behind it.
func TestConsistencyCheckFindsDanglingProviders(t *testing.T) {
	fixture := newRebindingFixture(t)
	psp := fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// Remove the record but leave the service pointing at it, which is the
	// state a crash between the two writes leaves behind.
	if err := fixture.raw.RemovePushServiceProvider(psp.Name()); err != nil {
		t.Fatalf("Could not remove the provider record: %v", err)
	}

	report := checkConsistency(t, fixture)
	dangling := findProblems(report, ProblemDanglingProvider)
	if len(dangling) != 1 {
		t.Fatalf("Expected one dangling-provider problem, got %d: %v", len(dangling), report.Problems)
	}
	if dangling[0].Subject != psp.Name() {
		t.Errorf("Expected the problem to name %q, got %q", psp.Name(), dangling[0].Subject)
	}
}

// TestConsistencyCheckFindsOrphanedProviders covers the other half of an
// interrupted write, and the state a push in flight across a provider
// replacement leaves behind: the record survives, but nothing points at it.
func TestConsistencyCheckFindsOrphanedProviders(t *testing.T) {
	fixture := newRebindingFixture(t)
	psp := fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// Remove it from the service's set but leave the record, which is what
	// crashing between the two writes the other way round leaves behind.
	if err := fixture.raw.RemovePushServiceProviderFromService(ServiceName, psp.Name()); err != nil {
		t.Fatalf("Could not remove the provider from the service: %v", err)
	}

	report := checkConsistency(t, fixture)
	orphaned := findProblems(report, ProblemOrphanedProvider)
	if len(orphaned) != 1 {
		t.Fatalf("Expected one orphaned-provider problem, got %d: %v", len(orphaned), report.Problems)
	}
	if orphaned[0].Subject != psp.Name() {
		t.Errorf("Expected the problem to name %q, got %q", psp.Name(), orphaned[0].Subject)
	}
	if orphaned[0].Service != ServiceName {
		t.Errorf("Expected the problem to name service %q, got %q", ServiceName, orphaned[0].Service)
	}
	if len(findProblems(report, ProblemDanglingProvider)) != 0 {
		t.Errorf("An orphaned provider is not a dangling one: %v", report.Problems)
	}
}

// TestConsistencyCheckScansPastOnePage guards the SCAN loop.
//
// SCAN returns a cursor and a page, and the page can be empty while the walk is
// still incomplete -- COUNT bounds the work redis does, not the rows it
// returns. A loop that stopped on an empty page, or that ignored the cursor,
// would pass every other test in this file: they all fit in one page. This one
// does not.
func TestConsistencyCheckScansPastOnePage(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")

	// Comfortably more delivery points than one SCAN page holds.
	const devices = scanKeysCount * 3
	for i := 0; i < devices; i++ {
		fixture.subscribe(t, fmt.Sprintf("devtoken-%d", i))
	}

	report := checkConsistency(t, fixture)
	if !report.Healthy() {
		t.Errorf("Expected no problems, got: %v", report.Problems)
	}
	if report.DeliveryPoints != devices {
		t.Errorf("Expected the scan to find all %d delivery points, got %d", devices, report.DeliveryPoints)
	}
	if report.Bindings != devices {
		t.Errorf("Expected the scan to find all %d bindings, got %d", devices, report.Bindings)
	}
}

// TestConsistencyCheckFindsStaleBindings covers a delivery point bound to a
// provider that has gone.
//
// While the binding is authoritative this is why a device stops receiving
// pushes, so the detail has to say how to bring it back. It is also the count
// that says what deriving the provider instead would fix.
func TestConsistencyCheckFindsStaleBindings(t *testing.T) {
	fixture := newRebindingFixture(t)
	original := fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	if err := fixture.client.RemovePushServiceProviderFromService(ServiceName, original); err != nil {
		t.Fatalf("Could not remove the original provider: %v", err)
	}
	fixture.addProvider(t, "second.cert")

	report := checkConsistency(t, fixture)
	stale := findProblems(report, ProblemStaleBinding)
	if len(stale) != 1 {
		t.Fatalf("Expected one stale-binding problem, got %d: %v", len(stale), report.Problems)
	}
	if stale[0].Subject != dp.Name() {
		t.Errorf("Expected the problem to name delivery point %q, got %q", dp.Name(), stale[0].Subject)
	}
	// The detail has to say what to do about it, or the report is just an alarm.
	if !strings.Contains(stale[0].Detail, "/addpsp") {
		t.Errorf("Expected the detail to say how to fix it, got %q", stale[0].Detail)
	}
}

// TestConsistencyCheckFindsOrphansAndLeakedCounters covers the debris left by
// the read path before it was fixed.
func TestConsistencyCheckFindsOrphansAndLeakedCounters(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	// Exactly what the old read path did: delete the record, keep everything
	// that pointed at it.
	if err := fixture.raw.RemoveDeliveryPoint(dp.Name()); err != nil {
		t.Fatalf("Could not remove the delivery point record: %v", err)
	}

	report := checkConsistency(t, fixture)
	if orphans := findProblems(report, ProblemOrphanedDeliveryPoint); len(orphans) != 1 {
		t.Errorf("Expected one orphaned delivery point, got %d: %v", len(orphans), report.Problems)
	}
	if leaked := findProblems(report, ProblemLeakedCounter); len(leaked) != 1 {
		t.Errorf("Expected one leaked counter, got %d: %v", len(leaked), report.Problems)
	}
}

// TestConsistencyCheckChangesNothing is the property that makes this safe to
// run against production.
//
// A check that repairs as it goes is a check nobody dares run on the database
// they are worried about, which is the only one worth checking.
func TestConsistencyCheckChangesNothing(t *testing.T) {
	fixture := newRebindingFixture(t)
	psp := fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")
	broken := fixture.subscribe(t, "devtoken-2")

	// Produce every kind of problem at once.
	if err := fixture.raw.RemoveDeliveryPoint(broken.Name()); err != nil {
		t.Fatalf("Could not remove a delivery point record: %v", err)
	}
	if err := fixture.raw.RemovePushServiceProvider(psp.Name()); err != nil {
		t.Fatalf("Could not remove the provider record: %v", err)
	}

	before := snapshotKeys(t, fixture)
	report := checkConsistency(t, fixture)
	if report.Healthy() {
		t.Fatal("Expected problems, so that this test is checking something")
	}
	// Twice, since an idempotence bug would show up on the second run.
	checkConsistency(t, fixture)
	after := snapshotKeys(t, fixture)

	if len(before) != len(after) {
		t.Errorf("The consistency check changed the database: %d keys before, %d after", len(before), len(after))
	}
	for key := range before {
		if !after[key] {
			t.Errorf("The consistency check deleted key %q", key)
		}
	}
	// And the surviving delivery point is untouched.
	if !fixture.keyExists(t, DeliveryPointPrefix+dp.Name()) {
		t.Error("The consistency check removed a delivery point")
	}
}

func snapshotKeys(t *testing.T, fixture *rebindingFixture) map[string]bool {
	t.Helper()
	keys, err := fixture.raw.client.Keys(context.Background(), "*").Result()
	if err != nil {
		t.Fatalf("Could not list keys: %v", err)
	}
	snapshot := make(map[string]bool, len(keys))
	for _, key := range keys {
		snapshot[key] = true
	}
	return snapshot
}

// TestConsistencyReportSummaryIsStable checks the summary is deterministic.
//
// The intended use is running the check, repairing something, running it again
// and diffing. Map iteration order would make that diff meaningless.
func TestConsistencyReportSummaryIsStable(t *testing.T) {
	report := &ConsistencyReport{Services: 2, Providers: 3, DeliveryPoints: 4}
	for _, problem := range []ConsistencyProblem{
		{Kind: ProblemLeakedCounter, Detail: "a"},
		{Kind: ProblemDuplicateProvider, Detail: "b"},
		{Kind: ProblemLeakedCounter, Detail: "c"},
		{Kind: ProblemStaleBinding, Detail: "d"},
	} {
		report.add(problem)
	}
	first := report.Summary()
	for i := 0; i < 20; i++ {
		if got := report.Summary(); got != first {
			t.Fatalf("Summary is not stable: %q then %q", first, got)
		}
	}
	if !strings.Contains(first, "leaked_counter=2") {
		t.Errorf("Expected the counts in the summary, got %q", first)
	}
}

// TestConsistencyReportCapsExamplesButNotCounts pins the bound that keeps a
// badly inconsistent database from taking the server down with it.
//
// The check exists to be run on a database somebody is already worried about,
// which is the one most likely to hold a finding per device. Assembling a
// million of them would exhaust the process's memory, then serialise into a
// response nobody could read, then fill the log. The counts are what an
// operator acts on, so those stay exact and the detail is capped.
func TestConsistencyReportCapsExamplesButNotCounts(t *testing.T) {
	report := new(ConsistencyReport)
	const found = MaxProblemsPerKind * 3
	for i := 0; i < found; i++ {
		report.add(ConsistencyProblem{Kind: ProblemLeakedCounter, Subject: fmt.Sprintf("dp-%d", i)})
	}
	// One of another kind, to show the cap is per kind rather than overall.
	report.add(ConsistencyProblem{Kind: ProblemDuplicateProvider, Subject: "svc"})

	if got := report.CountByKind()[ProblemLeakedCounter]; got != found {
		t.Errorf("Expected all %d findings to be counted, got %d", found, got)
	}
	if got := report.TotalProblems(); got != found+1 {
		t.Errorf("Expected %d findings in total, got %d", found+1, got)
	}
	if got := len(findProblems(report, ProblemLeakedCounter)); got != MaxProblemsPerKind {
		t.Errorf("Expected the examples to stop at %d, got %d", MaxProblemsPerKind, got)
	}
	if got := len(findProblems(report, ProblemDuplicateProvider)); got != 1 {
		t.Errorf("The cap is per kind: expected the lone duplicate-provider example, got %d", got)
	}
	if !strings.Contains(report.Summary(), fmt.Sprintf("leaked_counter=%d", found)) {
		t.Errorf("The summary must carry the true count, got %q", report.Summary())
	}
}

// TestCountByKindDoesNotAliasTheReport guards a copy that callers rely on:
// narrowing your own view of a report should not edit the report.
func TestCountByKindDoesNotAliasTheReport(t *testing.T) {
	report := new(ConsistencyReport)
	report.add(ConsistencyProblem{Kind: ProblemLeakedCounter, Subject: "dp"})

	counts := report.CountByKind()
	delete(counts, ProblemLeakedCounter)

	if report.CountByKind()[ProblemLeakedCounter] != 1 {
		t.Error("CountByKind handed out the report's own map")
	}
}

// TestConsistencyCheckFailsRatherThanReportingAHealthyDatabase covers the
// difference between "nothing is wrong" and "the scan did not finish".
//
// A read that fails mid-walk used to be skipped, so a transient redis error
// could produce a clean report -- the worst possible output, because the whole
// point of running this is to be able to believe a clean answer.
func TestConsistencyCheckFailsRatherThanReportingAHealthyDatabase(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	// Make reading one binding fail without deleting it: GET against a key
	// holding a set is WRONGTYPE, which is an error and not a missing key.
	key := ServiceDeliveryPointToPushServiceProviderPrefix + ServiceName + ":" + dp.Name()
	if err := fixture.raw.client.Del(context.Background(), key).Err(); err != nil {
		t.Fatalf("Could not clear the binding: %v", err)
	}
	if err := fixture.raw.client.SAdd(context.Background(), key, "wrong-type").Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	if _, err := fixture.client.CheckConsistency(); err == nil {
		t.Fatal("Expected a failed read to fail the check rather than be skipped")
	}
}
