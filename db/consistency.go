/*
 * Copyright 2013-2026 Uniqush Contributors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package db

import (
	"fmt"
	"sort"
	"strings"
)

// Kinds of problem CheckConsistency reports.
//
// Only the first is a correctness problem for pushing. The rest are debris:
// harmless to a running system, but each one is a question somebody would
// otherwise have to answer by reading redis by hand.
const (
	// ProblemDuplicateProvider is the one that matters. A service with two
	// providers of the same push service type has no single answer to "which
	// provider does this device send through", and srv-2-psp is an unordered
	// set, so a delivery point not covered by the stored binding would resolve
	// nondeterministically. Only data written before PR #201 can be in this
	// state; AddPushServiceProviderToService has rejected it since.
	//
	// This is the check the whole command exists for: deriving a delivery
	// point's provider rather than reading it is safe exactly when nothing
	// reports here, which is why the report ships before the derivation does.
	ProblemDuplicateProvider = "duplicate_provider"

	// ProblemDanglingProvider is a name in srv-2-psp with no record behind it.
	// /rmpsp removes both, so this means an interrupted write.
	ProblemDanglingProvider = "dangling_provider"

	// ProblemOrphanedProvider is the mirror image: a provider record that no
	// service's set points at, so nothing can push through it. Also an
	// interrupted write, and the state a push in flight across a provider
	// replacement can leave behind by updating the provider it started with.
	ProblemOrphanedProvider = "orphaned_provider"

	// ProblemStaleBinding is a srv.dp-2-psp entry naming a provider that no
	// longer exists. Expected, and harmless, after a provider's credentials
	// change: the binding is a tie-breaker rather than the answer. Reported so
	// the count can be seen to fall to zero once the index is retired.
	ProblemStaleBinding = "stale_binding"

	// ProblemBindingDisagrees is a srv.dp-2-psp entry naming a provider that
	// exists but is not the one the derivation picks. Worth knowing about
	// before the index is retired, since retiring it makes the derivation's
	// answer final.
	ProblemBindingDisagrees = "binding_disagrees"

	// ProblemOrphanedDeliveryPoint is a name in a subscriber's set with no
	// delivery.point record. Self-heals on the next read of that subscriber.
	ProblemOrphanedDeliveryPoint = "orphaned_delivery_point"

	// ProblemLeakedCounter is a delivery.point.counter with no delivery point.
	// Debris from the old read path, which deleted the record and left the
	// counter behind.
	ProblemLeakedCounter = "leaked_counter"
)

// ConsistencyProblem is one finding.
type ConsistencyProblem struct {
	Kind    string `json:"kind"`
	Service string `json:"service,omitempty"`
	Subject string `json:"subject,omitempty"`
	Detail  string `json:"detail"`
}

func (p ConsistencyProblem) String() string {
	if p.Service != "" {
		return fmt.Sprintf("[%s] service=%s %s", p.Kind, p.Service, p.Detail)
	}
	return fmt.Sprintf("[%s] %s", p.Kind, p.Detail)
}

// MaxProblemsPerKind bounds how many examples of one kind a report carries.
//
// Counts stay complete; this bounds only the detail. On a database with a
// million leaked counters the useful output is "a million leaked counters", not
// a million lines each saying so -- and assembling that list would exhaust the
// memory of the process the check was run to diagnose, then serialise it into
// an HTTP response too large to read. A check that takes the server down is
// worse than no check.
const MaxProblemsPerKind = 50

// ConsistencyReport is the result of a database scan.
type ConsistencyReport struct {
	Services       int `json:"services"`
	Providers      int `json:"push_service_providers"`
	DeliveryPoints int `json:"delivery_points"`
	Bindings       int `json:"delivery_point_bindings"`
	// Counts is every finding, by kind, whether or not Problems kept an example
	// of it. This is the number to act on.
	Counts map[string]int `json:"counts,omitempty"`
	// Problems holds up to MaxProblemsPerKind examples of each kind. Which
	// examples survive is whichever the scan met first, so treat them as
	// illustrations rather than as a set to work through.
	Problems []ConsistencyProblem `json:"problems"`
}

// add records a finding, keeping the first MaxProblemsPerKind of its kind.
func (r *ConsistencyReport) add(problem ConsistencyProblem) {
	if r.Counts == nil {
		r.Counts = make(map[string]int, 4)
	}
	r.Counts[problem.Kind]++
	if r.Counts[problem.Kind] <= MaxProblemsPerKind {
		r.Problems = append(r.Problems, problem)
	}
}

// Healthy reports whether anything needs attention.
func (r *ConsistencyReport) Healthy() bool { return len(r.Counts) == 0 }

// TotalProblems is how many findings there were, examples kept or not.
func (r *ConsistencyReport) TotalProblems() int {
	total := 0
	for _, count := range r.Counts {
		total += count
	}
	return total
}

// CountByKind summarises the findings.
//
// A copy, because callers filter it -- and a caller narrowing its own view of
// the report should not be editing the report.
func (r *ConsistencyReport) CountByKind() map[string]int {
	counts := make(map[string]int, len(r.Counts))
	for kind, count := range r.Counts {
		counts[kind] = count
	}
	return counts
}

// Summary is a one-line description, for a log.
func (r *ConsistencyReport) Summary() string {
	if r.Healthy() {
		return fmt.Sprintf("%d services, %d providers, %d delivery points: no problems found",
			r.Services, r.Providers, r.DeliveryPoints)
	}
	counts := r.CountByKind()
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	// Sorted so the same database produces the same line, which matters when
	// the output is being diffed across a repair.
	sort.Strings(kinds)

	described := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		described = append(described, fmt.Sprintf("%s=%d", kind, counts[kind]))
	}
	return fmt.Sprintf("%d services, %d providers, %d delivery points: %s",
		r.Services, r.Providers, r.DeliveryPoints, strings.Join(described, " "))
}

// CheckConsistency scans the database and reports what does not add up.
//
// Read-only: it changes nothing, so it is safe to run against production, and
// safe to run twice. Repairing is deliberately left to the operations that
// already exist -- /addpsp for a missing provider, a read for an orphaned
// delivery point -- because a repair that runs unattended on a database nobody
// has looked at yet is how a consistency check turns into an outage.
//
// It takes no lock, which is deliberate. dblock is what serialises every
// subscribe, unsubscribe and /addpsp in this process, and holding it across a
// walk of the whole keyspace would stop all of them for as long as the walk
// takes -- turning a diagnostic into the outage it is meant to help diagnose.
// Nothing here needs the consistency a lock would buy: the report is a
// description of a live database, and every finding is re-checked against the
// keys it names, so the worst a concurrent write can produce is a problem that
// had already been fixed by the time the report was printed.
func (f *pushDatabaseOpts) CheckConsistency() (*ConsistencyReport, error) {
	return f.db.CheckConsistency()
}
