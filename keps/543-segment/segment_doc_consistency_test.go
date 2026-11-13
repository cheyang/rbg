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
