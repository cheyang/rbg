#!/usr/bin/env bash
# bites-check.sh — prove the doc-consistency harness actually bites: apply the
# candidate fixes for F1-F5 to the KEP README, confirm all contract tests go
# green, then revert. The KEP doc is the only file touched, and it is always
# restored (trap), so the production diff stays empty.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
KEP=keps/502-workload-status-model/README.md
trap 'git checkout -- "$KEP"' EXIT

python3 - "$KEP" <<'PY'
import sys
p = "keps/502-workload-status-model/README.md"
s = open(p).read()
subs = [
    # F5: TOC entry
    ("  - [Phase Transition Principles](#phase-transition-principles)",
     "  - [Reason Taxonomy and Aggregation](#reason-taxonomy-and-aggregation)\n  - [Phase Transition Principles](#phase-transition-principles)"),
    # F1: anchor Preparing in the activity table
    ("| Recovery | `Recovering` | `Recovering` | `Recovering` |",
     "| Recovery | `Recovering` | `Recovering` | `Recovering` |\n| Preparation before workflow start | Not owned by RI | Not owned by RIS | `Preparing` |"),
    # F2: child-readiness vs activity clarification
    ("A current blocker outranks an activity reason because it explains why the current target is not converging. This reporting precedence does not determine controller execution order.",
     "A current blocker outranks an activity reason because it explains why the current target is not converging. This reporting precedence does not determine controller execution order. Child readiness does not outrank an activity reason: while a creation, update, scaling, or recovery workflow is in progress, the activity reason takes precedence and the not-ready children are described in message."),
    # F3: Progressing=False covers all config-rejection reasons
    ("| Progressing=False | `Converged`, `Paused`, `InvalidSpec`, `IncompatibleGangConfig`, `SchedulerUnsupported`, `Terminating` |",
     "| Progressing=False | `Converged`, `Paused`, `InvalidSpec`, `TemplateInvalid`, `DependencyCycle`, `IncompatibleGangConfig`, `SchedulerUnsupported`, `Terminating` |"),
    # F4: mark Ready [Changed]
    ("    - type: Ready # [Retained] Requires all roles to be ready, without additionally requiring target revision completion.",
     "    - type: Ready # [Changed] Requires all roles to be ready, without additionally requiring target revision completion; now also reports False while the object is terminating."),
]
for old, new in subs:
    if old not in s:
        print("bites-check: anchor not found (doc changed; harness-update needed):", old[:60])
        sys.exit(2)
    s = s.replace(old, new, 1)
open(p, "w").write(s)
print("candidate fixes applied")
PY

go test ./keps/502-workload-status-model/ -v
echo "bites-check: all green with candidate fixes; reverting"
