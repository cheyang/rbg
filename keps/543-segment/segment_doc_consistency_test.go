/*
Copyright 2026 The RBG Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package segment holds the review harness for KEP-543 documentation
// (PR #569). Additive only; no production or doc content is changed here.
package segment

import (
	"os"
	"regexp"
	"testing"
)

// Contract test for review finding F2.
//
// The normative "Group Assignment and Persistence" section defines the managed
// group label value as `<ruleID>-<groupIndex>` (ruleID = first 12 lowercase
// hex chars of SHA-256(ruleName)). Examples that still show the raw rule name
// as the segment label value prefix contradict that scheme.
//
// Polarity: CONTRACT — FAILS on the reviewed head, PASSES once the stale
// examples are rewritten to the ruleID form.
func TestVerifyKEP543_SegmentLabelExamplesUseRuleIDScheme(t *testing.T) {
	stale := regexp.MustCompile("`kv-cache-[0-9]+`|segment=kv-cache-[0-9]+")
	for _, f := range []string{"README.md", "README.zh-CN.md"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if m := stale.FindAllString(string(data), -1); len(m) > 0 {
			t.Errorf("%s contains stale rule-name-based segment label values %v; "+
				"normative scheme is <ruleID>-<groupIndex> (SHA-256-derived), see Group Assignment and Persistence",
				f, m)
		}
		// Guard against the check going vacuously green if the normative
		// section is renamed away.
		if !regexp.MustCompile("SHA-256").Match(data) {
			t.Errorf("%s no longer documents the SHA-256 ruleID scheme; update this harness", f)
		}
	}
}

// Contract test for review finding F3 (round 2, revision regression).
//
// Head d8d53364 introduced a zero-budget validation rule: an explicit
// maxUnavailable of 0 requires a positive maxSurge ("omitted or zero maxSurge
// is rejected"; "reconciliation reports percentage budgets that round down to
// zero without surge"; "a static conflict rather than a paused update").
// The Policy-admission unit-test bullet in the test plan still requires
// ACCEPTING a configuration whose effective maxUnavailable and maxSurge both
// resolve to zero, with paused-update semantics ("do not start updates") —
// the exact semantics the new rule disavows.
//
// Polarity: CONTRACT — FAILS on d8d53364, PASSES once the stale bullet is
// rewritten to the reject/report semantics (or removed).
func TestVerifyKEP543_ZeroBudgetTestPlanMatchesValidationRule(t *testing.T) {
	stale := map[string]*regexp.Regexp{
		"README.md":       regexp.MustCompile("(?i)accept a Segment rolling-update configuration[^\\n]*both resolve to zero"),
		"README.zh-CN.md": regexp.MustCompile("同时解析为 0 时仍接受"),
	}
	rule := map[string]*regexp.Regexp{
		"README.md":       regexp.MustCompile("requires a positive `maxSurge`"),
		"README.zh-CN.md": regexp.MustCompile("要求 `maxSurge` 为正"),
	}
	for f, staleRe := range stale {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if m := staleRe.FindAllString(string(data), -1); len(m) > 0 {
			t.Errorf("%s still requires accepting a zero-budget-without-surge configuration %v; "+
				"the normative rule now rejects (explicit) or reports (round-down) that combination",
				f, m)
		}
		if !rule[f].Match(data) {
			t.Errorf("%s no longer states the zero-budget validation rule; update this harness", f)
		}
	}
}

// Contract test for review finding F4 (round 2, doc accuracy).
//
// The discovery-owner paragraph added at d8d53364 claims discovery derives
// the address table "otherwise [from] the child RIS committed ordinals" once
// a child exists. pkg/discovery memberOrdinals shows the opposite for
// Segment-shaped members: the child-committed path requires excluded-ordinals
// or committed-skip-ordinals annotations, both of which Segment membership
// forbids, so the only reachable source is dense enumeration from RBG spec
// replicas (see pkg/discovery/verify_kep543_discovery_source_test.go). The
// misaligned-window invariant is guarded by the new fail-fast ordering, not
// by a committed-ordinals enumeration source.
//
// Polarity: CONTRACT — FAILS on d8d53364, PASSES once the enumeration-source
// clause is corrected (e.g. "from RBG role replicas; the child RIS committed
// ordinals only govern roles with excluded or committed-skip ordinals, which
// Segment members never carry").
func TestVerifyKEP543_DiscoveryEnumerationSourceIsAccurate(t *testing.T) {
	inaccurate := map[string]*regexp.Regexp{
		"README.md":       regexp.MustCompile("otherwise the child RIS committed ordinals"),
		"README.zh-CN.md": regexp.MustCompile("创建后使用子 RIS 的 committed ordinals"),
	}
	invariant := map[string]*regexp.Regexp{
		"README.md":       regexp.MustCompile("must enumerate only the aligned base-group target"),
		"README.zh-CN.md": regexp.MustCompile("必须只枚举对齐后的基础组目标"),
	}
	for f, badRe := range inaccurate {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if m := badRe.FindAllString(string(data), -1); len(m) > 0 {
			t.Errorf("%s mis-describes the existing discovery builder %v; for Segment members "+
				"(no excluded/committed-skip ordinals) memberOrdinals always enumerates dense RBG spec replicas",
				f, m)
		}
		if !invariant[f].Match(data) {
			t.Errorf("%s no longer states the discovery invariant; update this harness", f)
		}
	}
}
