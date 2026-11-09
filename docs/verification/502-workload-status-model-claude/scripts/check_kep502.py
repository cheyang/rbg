#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""check_kep502.py — deterministic verification harness for KEP-502 (PR #528).

PR under review: https://github.com/sgl-project/rbg/pull/528 (docs-only: adds
keps/502-workload-status-model/{README.md,kep.yaml}).

The deliverable under review is a *design document*, so the harness checks the
document's own contract: every factual claim the KEP makes about the current API
must hold, and its machine-readable reason taxonomy must be internally
consistent. Checks C1-C4 are CONTRACT tests for findings F1-F4 (fail == the
defect is present in the KEP). C5 verifies the KEP's motivation claims against
the base code (premise). C6 verifies the YAML examples parse.

Output: one line per check, "<check-id>\t<pass|fail>", on stdout (consumed by
re-verify.sh). Human-readable detail goes to stderr.

Usage:
    python3 -I check_kep502.py [--repo <repo-root>] [--kep <kep-readme-path>]
"""

from __future__ import print_function

import argparse
import os
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    yaml = None


RESULTS = []


def emit(cid, ok, detail):
    """Record a check result. ok is True/False."""
    RESULTS.append((cid, bool(ok), detail))


def read(path):
    with open(path, "r") as fh:
        return fh.read()


def json_tags(text):
    """All JSON field names declared in a Go types file."""
    return set(re.findall(r'json:"([a-zA-Z0-9]+)', text))


def camel_tokens(text):
    """Backticked CamelCase tokens (reasons / phases / condition types)."""
    return re.findall(r"`([A-Z][A-Za-z0-9]+)`", text)


def yaml_reason_tokens(text):
    """reason: values inside YAML example blocks."""
    blocks = re.findall(r"```yaml\n(.*?)```", text, re.S)
    reasons = []
    for b in blocks:
        reasons += re.findall(r"reason:\s*([A-Za-z0-9]+)", b)
    return reasons


# ---------------------------------------------------------------- C1 (F1)


def _field_list(raw):
    fields = []
    for part in re.split(r",|\band\b", raw):
        f = part.strip()
        if f and re.match(r"^[a-z][A-Za-z0-9]*$", f):
            fields.append(f)
    return fields


def check_c1(kep_text, repo):
    """Field-inventory contract: every status field the KEP claims to retain
    or preserve exists in the current API.

    The KEP's own convention (Design Details preamble) says 'Retained' is
    relative to the current API, so each named field must be a real field of
    RoleInstanceSet (v1alpha2 / v1alpha1) or RoleInstance (v1alpha2).
    """
    problems = []

    # Round-5 re-anchor: the 0b7285b7 redesign reworded the inventory from
    # "RIS retains <fields>." to "The existing counters and revision fields
    # remain the primary readiness and convergence facts:\n`f1`, ... and `fN`."
    # Accept both anchors (the contract is the field set, not the wording).
    m = re.search(r"RIS retains (.*?)\.", kep_text, re.S)
    m_new = re.search(r"remain the primary readiness and convergence facts:"
                      r"\s*(.*?)\s*This mirrors", kep_text, re.S)
    if not m and not m_new:
        problems.append("RIS retained-field inventory sentence not found "
                        "in KEP (neither 'RIS retains ...' nor the round-5 "
                        "'primary readiness and convergence facts' phrasing)")
    else:
        raw = m.group(1) if m else m_new.group(1)
        if m_new and not m:
            # new phrasing: fields are backticked
            fields = re.findall(r"`([a-zA-Z0-9]+)`", raw)
        else:
            fields = _field_list(raw)
            if m_new:
                fields = sorted(set(fields) |
                                set(re.findall(r"`([a-zA-Z0-9]+)`", raw)))
        api_fields = set()
        for rel in ("api/workloads/v1alpha2/roleinstanceset_types.go",
                    "api/workloads/v1alpha1/instanceset_types.go"):
            p = os.path.join(repo, rel)
            if os.path.exists(p):
                api_fields |= json_tags(read(p))
        missing = [f for f in fields if f not in api_fields]
        if missing:
            problems.append("RIS retained fields missing from current API: %s"
                            % ", ".join(missing))

    m2 = re.search(r"Preserve (restartCount.*?)\.", kep_text, re.S)
    if not m2:
        problems.append("'Preserve restartCount ...' sentence not found in KEP")
    else:
        fields = _field_list(m2.group(1))
        p = os.path.join(repo, "api/workloads/v1alpha2/roleinstance_types.go")
        api_fields = json_tags(read(p)) if os.path.exists(p) else set()
        missing = [f for f in fields if f not in api_fields]
        if missing:
            problems.append("RI preserved fields missing from current API: %s"
                            % ", ".join(missing))

    emit("C1-retained-fields-exist", not problems,
         "; ".join(problems) if problems
         else "RIS 'retains' and RI 'Preserve' inventories all exist in the "
              "current API")


# ---------------------------------------------------------------- C2 (F2)


def check_c2(kep_text):
    """F2: every reason in the RBG Progressing=True candidate row is defined
    or used elsewhere in the document (>= 2 distinct lines of occurrence)."""
    rows = re.findall(r"^\|\s*Progressing=True\s*\|(.*)\|\s*$", kep_text, re.M)
    if not rows:
        emit("C2-progressing-reasons-defined", False,
             "Progressing=True candidate-reason row not found")
        return
    reasons = re.findall(r"`([A-Za-z]+)`", rows[0])
    undefined = []
    for r in reasons:
        lines = [ln for ln in kep_text.splitlines() if r in ln]
        if len(lines) < 2:
            undefined.append("%s (%d line(s))" % (r, len(lines)))
    emit("C2-progressing-reasons-defined", not undefined,
         "candidate reasons: %s; not defined/used elsewhere: %s"
         % (", ".join(reasons), ", ".join(undefined) if undefined else "(none)"))


# ---------------------------------------------------------------- C3 (F3)


def check_c3(kep_text):
    """F3: no two distinct documented reasons differ only by one inserted 's'
    without an acknowledging line that mentions both spellings."""
    tokens = set(camel_tokens(kep_text)) | set(yaml_reason_tokens(kep_text))
    lines = kep_text.splitlines()
    collisions = []
    for a in sorted(tokens):
        for i in range(len(a) + 1):
            b = a[:i] + "s" + a[i:]
            if b in tokens and b != a:
                ack = any(a in ln and b in ln for ln in lines)
                if not ack:
                    collisions.append("%s vs %s" % (a, b))
    emit("C3-reason-collision-ack", not collisions,
         "unacknowledged near-colliding reason pairs: %s"
         % ("; ".join(collisions) if collisions else "(none)"))


# ---------------------------------------------------------------- C4 (F4)


def check_c4(kep_text, repo):
    """F4: the KEP's candidate reasons for aggregated RI readiness must match,
    case-sensitively, the reason strings the current RI controller writes for
    the RoleInstanceReady condition."""
    m = re.search(r"aggregated RI readiness are (.*?)\.", kep_text)
    if not m:
        emit("C4-ri-readiness-reasons-match-code", False,
             "'Candidate reasons for aggregated RI readiness' sentence not found")
        return
    kep_reasons = set()
    for part in re.split(r",|\band\b", m.group(1)):
        p = part.strip()
        if p:
            kep_reasons.add(p)
    p = os.path.join(repo, "pkg/reconciler/roleinstance/instance_status.go")
    code_reasons = set()
    if os.path.exists(p):
        code_reasons = set(
            re.findall(r'instanceReady\.Reason = "([^"]+)"', read(p)))
    diff = kep_reasons.symmetric_difference(code_reasons)
    emit("C4-ri-readiness-reasons-match-code", not diff,
         "KEP: {%s} vs code RoleInstanceReady reasons: {%s}; diff: %s"
         % (", ".join(sorted(kep_reasons)), ", ".join(sorted(code_reasons)),
            ", ".join(sorted(diff)) if diff else "(none)"))


# ---------------------------------------------------------------- C5 (P0)


def walk_go_files(root):
    for dirpath, _dirnames, filenames in os.walk(root):
        for fn in filenames:
            if fn.endswith(".go") and not fn.endswith("_test.go"):
                yield os.path.join(dirpath, fn)


def check_c5(repo):
    """Premise/motivation: the KEP's factual claims about the current API hold.
    - RBG / RIS / RI have no status.phase today;
    - controllers never write RBG Progressing / RollingUpdateInProgress;
    - RIS and RI do declare FailedScale / FailedUpdate conditions.
    """
    problems = []
    for rel in ("api/workloads/v1alpha2/rolebasedgroup_types.go",
                "api/workloads/v1alpha2/roleinstanceset_types.go",
                "api/workloads/v1alpha2/roleinstance_types.go"):
        p = os.path.join(repo, rel)
        if os.path.exists(p) and re.search(r'json:"phase', read(p)):
            problems.append("%s already declares status.phase" % rel)
    writers = []
    for base in ("internal", "pkg"):
        root = os.path.join(repo, base)
        if not os.path.isdir(root):
            continue
        for path in walk_go_files(root):
            text = read(path)
            if re.search(r"RoleBasedGroupProgressing\b", text):
                writers.append(path)
            if re.search(r"RoleBasedGroupRollingUpdateInProgress\b", text):
                writers.append(path)
            if re.search(r'"Progressing"|"RollingUpdateInProgress"', text):
                writers.append(path)
    if writers:
        problems.append("controllers write RBG Progressing/"
                        "RollingUpdateInProgress: %s" % ", ".join(writers[:5]))
    ris = os.path.join(repo, "api/workloads/v1alpha2/roleinstanceset_types.go")
    ri = os.path.join(repo, "api/workloads/v1alpha2/roleinstance_types.go")
    for p, name in ((ris, "RIS"), (ri, "RI")):
        if os.path.exists(p):
            t = read(p)
            if "FailedScale" not in t or "FailedUpdate" not in t:
                problems.append("%s does not declare FailedScale/FailedUpdate" % name)
    emit("C5-premise-motivation-claims", not problems,
         "KEP motivation claims vs base code: %s"
         % ("; ".join(problems) if problems else "all verified accurate"))


# ---------------------------------------------------------------- C6


def check_c6(kep_path, yaml_dir):
    if yaml is None:
        emit("C6-yaml-examples-parse", False, "PyYAML not available")
        return
    text = read(kep_path)
    blocks = re.findall(r"```yaml\n(.*?)```", text, re.S)
    bad = []
    for i, b in enumerate(blocks):
        try:
            yaml.safe_load(b)
        except Exception as exc:  # noqa: BLE001
            bad.append("block %d: %s" % (i, exc))
    kep_yaml = os.path.join(yaml_dir, "kep.yaml")
    try:
        data = yaml.safe_load(read(kep_yaml))
        num_ok = data.get("kep-number") == 502
    except Exception as exc:  # noqa: BLE001
        bad.append("kep.yaml: %s" % exc)
        num_ok = False
    if not num_ok:
        bad.append("kep.yaml kep-number != 502")
    emit("C6-yaml-examples-parse", not bad,
         "%d/%d YAML example blocks parse; kep.yaml: %s"
         % (len(blocks) - len([b for b in bad if b.startswith("block")]),
            len(blocks), "OK" if num_ok else "FAIL"))


# ---------------------------------------------------------------- C8 (N1)

def check_c8(kep_text):
    """Round-2 contract (revision regression N1): every statement in the KEP
    that names the *inputs to RBG phase transitions* must use one consistent
    source vocabulary. The revision at 6335eac rewired recovery derivation to
    "lower-level conditions" (README lines ~463, ~502: "RBG derives recovery
    facts from lower-level conditions, not a RIS `Recovering` phase") while
    leaving the Goals item and the RoleBasedGroup intro saying transitions are
    "based on ... lower-level phases". A reader of the RBG intro would look for
    child phase values as transition inputs -- the very thing the revision
    disclaims. Fail when both phrasings occur.
    Polarity: contract (fail == inconsistency present).
    """
    phase_lines = [(i + 1, l.strip()) for i, l in enumerate(kep_text.splitlines())
                   if re.search(r"lower-level phases", l)
                   and not re.search(r"conditions", l)]
    cond_lines = [(i + 1, l.strip()) for i, l in enumerate(kep_text.splitlines())
                  if re.search(r"lower-level conditions", l)]
    problems = []
    if phase_lines and cond_lines:
        problems.append(
            "RBG transition inputs described inconsistently: "
            "'lower-level phases' (without conditions) at line(s) %s vs "
            "'lower-level conditions' at line(s) %s"
            % (",".join(str(n) for n, _ in phase_lines),
               ",".join(str(n) for n, _ in cond_lines)))
    elif not phase_lines and not cond_lines:
        problems.append("no 'lower-level phases/conditions' statement found")
    emit("C8-rbg-transition-input-consistency", not problems,
         "; ".join(problems) if problems
         else "RBG phase-transition input statements use one vocabulary "
              "(phases-only claims: %d, conditions claims: %d)"
              % (len(phase_lines), len(cond_lines)))


# ---------------------------------------------------------------- C10 (C4457744212)


def check_c10(kep_text):
    """Round-4 contract (Copilot advisory C4457744212, fixed at da818779):
    the reporting-priority list resolves *between* reason families, so when
    heterogeneous Activity facts coexist (one role Creating, another
    Recovering, another updating), the choice of RBG Progressing=True reason
    inside the Activity family was underdetermined. The round-4 delta adds a
    deterministic within-Activity precedence. This check guards it:

      1. a "Within the Activity family, RBG uses this deterministic
         precedence:" statement with a fenced list of exactly the RBG
         activity reasons from the activity/child-readiness family table
         (Creating, Recovering, ScalingOrUpdating), no duplicates;
      2. the one-dominant-reason rule: the highest-precedence activity reason
         is used and all activities are described in message;
      3. the rationale's pairwise orderings ("Creating outranks recovery",
         "Recovering outranks ordinary target changes") agree with the list
         order (a contradiction between prose and list fails);
      4. the unit-test plan covers the concurrent heterogeneous combinations
         (Recovering-over-update and Creating-over-update/recovery), not only
         the homogeneous scaling+update case.

    Polarity: contract (fail == precedence unstated, incomplete, or
    inconsistent with its own rationale / test plan).
    """
    problems = []
    lines = kep_text.splitlines()

    # (1) the fenced precedence list
    m = re.search(r"Within the Activity family, RBG uses this deterministic "
                  r"precedence:\s*```\s*text\s*\n(.*?)```",
                  kep_text, re.S)
    if m is None:
        problems.append("'Within the Activity family ... precedence' fenced "
                        "list not found")
        order = []
    else:
        order = [l.strip() for l in m.group(1).splitlines() if l.strip()]

    # expected set: RBG activity reasons from the family table (RBG column,
    # activity rows; child readiness is a different family)
    expected = set()
    try:
        start = next(i for i, l in enumerate(lines)
                     if "Activity and child-readiness reasons" in l)
    except StopIteration:
        expected = None
    if expected is None:
        problems.append("activity/child-readiness family table not found")
    else:
        activity_rows = ("Initial creation", "Update",
                         "Instance-count scaling", "Recovery")
        in_table = False
        for l in lines[start + 1:]:
            s = l.strip()
            if not s:
                if in_table:
                    break
                continue
            if not s.startswith("|"):
                if in_table:
                    break
                continue
            in_table = True
            cells = [c.strip() for c in s.strip("|").split("|")]
            if len(cells) >= 4 and cells[0] in activity_rows:
                for tok in re.findall(r"`([A-Za-z0-9]+)`", cells[3]):
                    expected.add(tok)
        if not expected:
            problems.append("no RBG activity reasons parsed from the family "
                            "table")
        else:
            if set(order) != expected:
                problems.append(
                    "precedence list [%s] is not exactly the family-table RBG "
                    "activity reasons [%s]"
                    % (", ".join(order) or "(none)",
                       ", ".join(sorted(expected))))
            elif len(order) != len(expected):
                problems.append("precedence list has duplicates: %s"
                                % ", ".join(order))

    # (2) one-dominant-reason + message rule
    if not re.search(r"When multiple Activity facts are present, RBG uses "
                     r"the highest-precedence activity reason", kep_text):
        problems.append("'highest-precedence activity reason' rule not "
                        "stated")
    if not re.search(r"describes (?:all|every) (?:concurrent )?activit",
                     kep_text) and not re.search(
            r"describes all activities in message", kep_text):
        problems.append("all-activities-in-message clause not stated")

    # (3) rationale pairwise orderings vs list order
    if len(order) == len(set(order)) and len(order) >= 3:
        idx = {t: i for i, t in enumerate(order)}
        pairs = []
        if re.search(r"`Creating` outranks recovery", kep_text):
            pairs.append(("Creating", "Recovering"))
        if re.search(r"`Recovering` outranks ordinary target changes",
                     kep_text):
            pairs.append(("Recovering", "ScalingOrUpdating"))
        for hi, lo in pairs:
            if idx.get(hi, 99) > idx.get(lo, -1):
                problems.append(
                    "rationale says %s outranks %s but list order is %s"
                    % (hi, lo, " > ".join(order)))

    # (4) test-plan coverage of the heterogeneous combinations
    covered = (
        re.search(r"one role is `Recovering` and another is updating",
                  kep_text)
        and re.search(r"one role is `Creating` and another is (?:updating or "
                      r"recovering|updating|recovering)", kep_text))
    if not covered:
        problems.append("unit-test plan does not cover the concurrent "
                        "heterogeneous activity combinations "
                        "(Recovering-over-update, Creating-over-update)")

    emit("C10-rbg-activity-precedence", not problems,
         "; ".join(problems) if problems
         else "within-Activity precedence stated as [%s]; one-dominant-reason "
              "and message rules present; rationale orderings agree; "
              "concurrent combinations covered in the test plan"
              % " > ".join(order) or "(none)")


# ---------------------------------------------------------------- C9 (N2)

def check_c9(kep_text):
    """Round-2 contract (N2, pre-existing nit): the RIS Progressing prose
    sentence enumerating activity reasons ('Activity reasons may include ...'
    in rounds 1-2, 'Activity reasons are ...' from f622d642 on) must list
    every RIS activity reason defined in the activity/child-readiness family
    table (rows: Initial creation -> Creating, Update -> RollingUpdate,
    Instance-count scaling -> ScalingUp/ScalingDown, Recovery -> Recovering).
    An implementer writing RIS's reason validator needs the complete set in
    one place; today it is split across prose and two tables.
    Round-3 re-anchor: the fix reworded 'may include' to the definitive
    'are'; the anchor now accepts both phrasings so the contract keeps
    testing the enumeration, not the wording.
    Polarity: contract (fail == enumeration incomplete).
    """
    # collect RIS reasons from the activity/child-readiness family table
    lines = kep_text.splitlines()
    try:
        start = next(i for i, l in enumerate(lines)
                     if "Activity and child-readiness reasons" in l)
    except StopIteration:
        emit("C9-ris-activity-reasons-complete", False,
             "activity/child-readiness family table not found")
        return
    activity_rows = ("Initial creation", "Update", "Instance-count scaling",
                     "Recovery")
    expected = set()
    in_table = False
    for l in lines[start + 1:]:
        s = l.strip()
        if not s:
            if in_table:
                break
            continue  # blank line before the table header
        if not s.startswith("|"):
            if in_table:
                break
            continue  # prose/other content before the table
        in_table = True
        cells = [c.strip() for c in s.strip("|").split("|")]
        if len(cells) >= 3 and cells[0] in activity_rows:
            for tok in re.findall(r"`([A-Za-z0-9]+)`", cells[2]):
                expected.add(tok)
    m = re.search(r"Activity reasons (?:may include|are) (.*?)[.;]",
                  kep_text, re.S)
    if not m:
        emit("C9-ris-activity-reasons-complete", False,
             "'Activity reasons may include/are ...' sentence not found")
        return
    listed = set(re.findall(r"`([A-Za-z0-9]+)`", m.group(1)))
    missing = sorted(expected - listed)
    emit("C9-ris-activity-reasons-complete", not missing,
         "RIS activity reasons from family table: %s; prose lists: %s; "
         "missing from prose: %s"
         % (", ".join(sorted(expected)) or "(none)",
            ", ".join(sorted(listed)) or "(none)",
            ", ".join(missing) or "(none)"))


# ------------------------------------------------- round-5 checks (0b7285b7)
#
# The round-5 delta (da818779..0b7285b7) is a redesign: RBG and RIS drop
# status.phase (conditions + counters only), RIS drops its proposed Ready
# condition, the Terminating reasons leave the reason tables, and several NEW
# factual claims about current code appear. C11-C14 guard the new content as
# contracts (green at head when the redesign is coherent); C15/C16 are repro
# probes for the two open round-5 findings (red at head == defect present).


def check_c11(kep_text, repo):
    """Round-5 code-factual claims (AC extension): the redesign's new
    statements about main-branch code must be accurate.

    Claims verified (KEP text present AND repo mechanics present):
      1. RI controller rebuilds RoleInstanceReady / RoleInstanceAllPodsReady
         with the current time and replaces the condition list, so every
         status write resets lastTransitionTime.
      2. Stateless RIS counts an RI ready via RoleInstanceReady AND every
         spec.readinessGates condition; stateful mode checks only
         RoleInstanceReady.
      3. The lifecycle-hook label rbg.workloads.x-k8s.io/role-instance-
         lifecycle-state keeps values Normal / PreparingUpdate / Updating /
         Updated / PreparingDelete.
      4. CoordinatedPolicy lowers a role's partition stepwise (stepPartition).
    Polarity: contract (fail == a KEP claim is missing or the code no longer
    matches it).
    """
    problems = []

    def claim(kep_pat, code_rel, code_pat, what):
        if not re.search(kep_pat, kep_text):
            problems.append("KEP claim missing: %s" % what)
            return
        p = os.path.join(repo, code_rel)
        body = read(p) if os.path.exists(p) else ""
        if not re.search(code_pat, body):
            problems.append("code does not match KEP claim: %s (%s)"
                            % (what, code_rel))

    # 1. RI condition rebuild + timestamp reset
    claim(r"rebuilds `RoleInstanceReady` / `RoleInstanceAllPodsReady` with "
          r"the current time",
          "pkg/reconciler/roleinstance/instance_status.go",
          r"LastTransitionTime:\s*metav1\.NewTime\(time\.Now\(\)\)",
          "RI conditions rebuilt with time.Now()")
    if re.search(r"replaces the condition list", kep_text):
        p = os.path.join(repo,
                         "pkg/reconciler/roleinstance/instance_status.go")
        body = read(p) if os.path.exists(p) else ""
        if not re.search(r"newStatus\.Conditions = conditions", body):
            problems.append("code does not replace the condition list "
                            "(setInstanceConditions)")
    else:
        problems.append("KEP claim missing: replaces the condition list")

    # 2. stateless vs stateful RI-ready counting
    claim(r"stateless mode requires both `RoleInstanceReady` and every "
          r"`spec\.readinessGates` condition to be True, while stateful mode "
          r"checks only `RoleInstanceReady`",
          "pkg/reconciler/roleinstanceset/statelessmode/utils/utils.go",
          r"for _, rg := range instance\.Spec\.ReadinessGates",
          "stateless gate loop")
    p_stateful = os.path.join(repo,
        "pkg/reconciler/roleinstanceset/statefulmode/"
        "stateful_instance_set_utils.go")
    if os.path.exists(p_stateful):
        body = read(p_stateful)
        m = re.search(r"func isHealthy\(.*?\n(.*?)\n}", body, re.S)
        if not (m and "ReadinessGates" not in m.group(1)
                and "RoleInstanceReady" in m.group(1)):
            problems.append("stateful isHealthy no longer checks only "
                            "RoleInstanceReady")
    else:
        problems.append("stateful helper file not found")

    # 3. hook protocol ownership (round-6 Harness-update: 39e2ecdf replaced
    #    the in-doc lifecycle-state label enumeration with a delegation to
    #    KEP-527; the code-side constants are still verified as context —
    #    today's protocol label and its five values exist in the repo).
    if not re.search(r"hook-state labels and finalizer handshake defined "
                     r"by KEP-527", kep_text):
        problems.append("KEP claim missing: hook protocol ownership "
                        "(hook-state labels / finalizer handshake, "
                        "KEP-527)")
    p_const = os.path.join(repo, "api/workloads/constants/constants.go")
    p_annot = os.path.join(repo, "api/workloads/constants/annotation.go")
    consts = ""
    for p in (p_const, p_annot):
        if os.path.exists(p):
            consts += read(p)
    for v in ("Normal", "PreparingUpdate", "Updating", "Updated",
              "PreparingDelete"):
        if not re.search(r'RoleInstanceLifecycleState%s\s+RoleInstance'
                         r'LifecycleStateType = "%s"' % (v, v), consts):
            problems.append("lifecycle state value missing from code: %s" % v)
    if not re.search(r'RoleInstanceSetLifecycleStateKey = RBGPrefix \+ '
                     r'"role-instance-lifecycle-state"', consts):
        problems.append("lifecycle-state label key constant not found")

    # 4. CoordinatedPolicy stepwise partition
    claim(r"CoordinatedPolicy may lower a role's RIS partition or "
          r"coordinated scaling targets one step at a time",
          "internal/controller/workloads/rolebasedgroup_controller.go",
          r"stepPartition :=", "CoordinatedPolicy stepwise partition")

    emit("C11-round5-code-claims", not problems,
         "; ".join(problems) if problems
         else "all round-5 code-factual claims verified against the repo")


def check_c12(kep_text):
    """F11 guard (round-4 open item, fixed at 0b7285b7): the RIS-layer
    within-Activity tie-break for Progressing must be stated — the RIS
    activity precedence with recovery outranking active update and scaling —
    plus the recovery-overlaps-workflow unit-test bullet.
    Polarity: contract (fail == RIS tie-break unstated).
    """
    problems = []
    if not re.search(r"Activity precedence for RIS `Progressing` is "
                     r"`Creating`, `Recovering`, `RollingUpdate`, and "
                     r"`ScalingUp` / `ScalingDown`", kep_text):
        problems.append("RIS activity-precedence statement not found")
    if not re.search(r"Recovery outranks active update and scaling",
                     kep_text):
        problems.append("'Recovery outranks active update and scaling' "
                        "not stated")
    if not re.search(r"When RI recovery overlaps an active RIS update or "
                     r"scaling workflow, RIS `Progressing` uses `Recovering`",
                     kep_text):
        problems.append("recovery-overlap test bullet not found")
    emit("C12-ris-activity-precedence", not problems,
         "; ".join(problems) if problems
         else "RIS within-Activity precedence stated with recovery "
              "outranking update/scaling; recovery-overlap test bullet "
              "present")


def check_c13(kep_text):
    """F12 guard (round-4 open item; round-6 Harness-update after the fix at
    39e2ecdf reworded the guarded lines): `Creating` must be scoped to
    initial establishment at BOTH layers, and a post-establishment missing /
    replacement child must be classified under the workflow that requires it
    — Recovering for replacing an established child, ScalingOrUpdating /
    ScalingUp for a target that adds one, RollingUpdate for update-created
    instances (this also retires Copilot C4495569498's scale-up
    over-capture, pinned by the 2 -> 3 test bullet).
    Polarity: contract (fail == scoping or classification unstated at
    either layer). Fails at 0b7285b7 (round-5 definitional wording only),
    passes at 39e2ecdf.
    """
    problems = []
    # RBG layer: establishment scoping + post-establishment classification
    if not re.search(r"`Creating` is used only during initial RBG "
                     r"establishment", kep_text):
        problems.append("RBG Creating not scoped to initial establishment")
    if not re.search(r"Once the RBG is established, a missing child is not "
                     r"`Creating`", kep_text):
        problems.append("RBG post-establishment missing-child classification "
                        "not stated")
    if not re.search(r"such as `Recovering` for replacing an established "
                     r"child or `ScalingOrUpdating` for a target that adds "
                     r"one", kep_text):
        problems.append("RBG replacement->Recovering / scale-up->"
                        "ScalingOrUpdating mapping not stated")
    # RIS layer
    if not re.search(r"`Creating` is used only during initial set "
                     r"establishment", kep_text):
        problems.append("RIS Creating not scoped to initial establishment")
    if not re.search(r"Once the set is established, a required RI that does "
                     r"not yet exist is not `Creating`", kep_text):
        problems.append("RIS post-establishment missing-RI classification "
                        "not stated")
    if not re.search(r"`ScalingUp` when scaling adds instances, "
                     r"`RollingUpdate` when update work creates or replaces "
                     r"instances", kep_text):
        problems.append("RIS scale-up / update classification not stated")
    # test-plan bullet pinning the scale-up case (C4495569498)
    if not (re.search(r"reports `ScalingUp`", kep_text)
            and u"2 → 3" in kep_text):
        problems.append("scale-up test bullet (2 -> 3 reports ScalingUp) "
                        "not stated")
    emit("C13-replacement-mapping-stated", not problems,
         "; ".join(problems) if problems
         else "Creating scoped to initial establishment at both layers; "
              "post-establishment missing children classified under the "
              "requiring workflow (incl. scale-up)")


def check_c14(kep_text):
    """Round-5 redesign scope guard: RBG and RIS add no status.phase; RI
    keeps its optional phase. Fail on any statement that RBG or RIS
    adds/persists a lifecycle phase, or on missing negative statements.
    Polarity: contract (fail == phase scope inconsistent).
    """
    problems = []
    bad = re.findall(r"RBG adds `status\.phase`|"
                     r"RIS adds a persisted `?status\.phase`|"
                     r"RIS adds `status\.phase`", kep_text)
    if bad:
        problems.append("statement claims RBG/RIS adds status.phase: %s"
                        % "; ".join(bad[:3]))
    if not re.search(r"RBG (?:and RIS )?do(?:es)? not add `status\.phase`",
                     kep_text):
        problems.append("'RBG does not add status.phase' statement missing")
    if not re.search(r"RIS does not add `status\.phase`", kep_text):
        problems.append("'RIS does not add status.phase' statement missing")
    if not re.search(r"RI adds optional `status\.phase`", kep_text):
        problems.append("'RI adds optional status.phase' statement missing")
    emit("C14-phase-scope-consistency", not problems,
         "; ".join(problems) if problems
         else "phase scope consistent: RBG/RIS condition-only, RI phase only")


def check_c15(kep_text):
    """F15 repro (round-5 open, minor): the preCheck-failure status-write
    rule contradicts the Accepted=False -> Progressing=False mapping.
    The test plan says the parent writes observedGeneration=N together with
    Progressing=True 'including preCheck failures', while the Accepted
    semantics require Progressing=False with the matching incompatibility
    reason when Accepted=False. Both are normative; nothing reconciles them.
    Polarity: contract (fail == the contradiction is present; passes once
    the clause is scoped/reworded or a sequencing statement is added).
    """
    problems = []
    precheck_true = re.search(r"including pre[Cc]heck failures[^.;]*"
                              r"`Progressing=True`", kep_text)
    accepted_false_map = re.search(r"When `Accepted=False`, "
                                   r"`Progressing=False` uses the matching",
                                   kep_text)
    if precheck_true and accepted_false_map:
        # look for an explicit reconciliation (sequencing / scoping)
        reconciled = re.search(r"pre[Cc]heck[^.;]*(?:then|before the "
                               r"compatibility|intermediate|flip)", kep_text)
        if not reconciled:
            problems.append("preCheck-failure clause prescribes "
                            "Progressing=True while Accepted=False "
                            "prescribes Progressing=False; no sequencing "
                            "statement reconciles them")
    elif not accepted_false_map:
        problems.append("Accepted=False -> Progressing=False mapping not "
                        "found (harness anchor may be stale)")
    emit("C15-precheck-write-consistency", not problems,
         "; ".join(problems) if problems
         else "preCheck-failure status-write rule consistent with "
              "Accepted=False semantics")


def check_c16(kep_text):
    """F12 residual (round-5 Claude F14, folded into F12 by synthesis;
    round-6 Harness-update after 39e2ecdf): the 'established' discriminator
    must be derivable from persisted / current facts, so a controller —
    including after restart, when Progressing.reason is recomputed 'from
    current facts' — can decide establishment. Round-6 fix: the RIS clause
    names persisted facts ('an established revision or established RIs for
    its target') and the RBG clause names the target's required roles.
    Polarity: contract (fail == discriminator not tied to persisted facts).
    """
    problems = []
    has_scoping = re.search(r"`Creating` is used only during initial "
                            r"(?:RBG|set) establishment", kep_text)
    if not has_scoping:
        problems.append("establishment scoping not found (harness anchor "
                        "may be stale — see C13)")
    else:
        disc = (re.search(r"established revision or established RIs for "
                          r"its target", kep_text)
                and re.search(r"required roles have been established",
                              kep_text))
        if not disc:
            problems.append("establishment discriminator not tied to "
                            "persisted facts (established revision / "
                            "established RIs / required roles established)")
        if not re.search(r"recompute `Progressing\.reason` from current "
                         r"facts", kep_text):
            problems.append("restart recompute rule not stated")
    emit("C16-replacement-discriminator", not problems,
         "; ".join(problems) if problems
         else "establishment discriminator tied to persisted facts "
              "(RIS: established revision / established RIs; RBG: required "
              "roles established)")


def check_c17(kep_text):
    """C4457744212 design-content guard (split from C10 in round 5): the
    within-Activity precedence DESIGN CONTENT only — fenced list over exactly
    the family-table RBG activity reasons, the one-dominant-reason + message
    rules, rationale not contradicting the list order. The test-plan
    coverage clause lives in C10 (tracked as F13 after the round-5 delta
    dropped the Creating-combination bullet), so this check isolates the
    design content that the round-4 fix delivered and that must not regress.
    Polarity: contract (fail == precedence design content missing or
    self-contradictory).
    """
    problems = []
    lines = kep_text.splitlines()
    m = re.search(r"Within the Activity family, RBG uses this deterministic "
                  r"precedence:\s*```\s*text\s*\n(.*?)```", kep_text, re.S)
    if m is None:
        problems.append("'Within the Activity family ... precedence' fenced "
                        "list not found")
        order = []
    else:
        order = [l.strip() for l in m.group(1).splitlines() if l.strip()]
    expected = set()
    try:
        start = next(i for i, l in enumerate(lines)
                     if "Activity and child-readiness reasons" in l)
    except StopIteration:
        expected = None
    if expected is None:
        problems.append("activity/child-readiness family table not found")
    else:
        activity_rows = ("Initial creation", "Update",
                         "Instance-count scaling", "Recovery")
        in_table = False
        for l in lines[start + 1:]:
            s = l.strip()
            if not s:
                if in_table:
                    break
                continue
            if not s.startswith("|"):
                if in_table:
                    break
                continue
            in_table = True
            cells = [c.strip() for c in s.strip("|").split("|")]
            if len(cells) >= 4 and cells[0] in activity_rows:
                for tok in re.findall(r"`([A-Za-z0-9]+)`", cells[3]):
                    expected.add(tok)
        if not expected:
            problems.append("no RBG activity reasons parsed from the family "
                            "table")
        elif set(order) != expected:
            problems.append("precedence list [%s] is not exactly the "
                            "family-table RBG activity reasons [%s]"
                            % (", ".join(order) or "(none)",
                               ", ".join(sorted(expected))))
    if not re.search(r"When multiple Activity facts are present, RBG uses "
                     r"the highest-precedence activity reason", kep_text):
        problems.append("'highest-precedence activity reason' rule not "
                        "stated")
    if not (re.search(r"describes (?:all|every) (?:concurrent )?activit",
                      kep_text)
            or re.search(r"describes all activities in message", kep_text)):
        problems.append("all-activities-in-message clause not stated")
    if len(order) == len(set(order)) and len(order) >= 3:
        idx = {t: i for i, t in enumerate(order)}
        for hi, lo in (("Creating", "Recovering"),
                       ("Recovering", "ScalingOrUpdating")):
            if (re.search(r"`%s` outranks" % hi, kep_text)
                    and idx.get(hi, 99) > idx.get(lo, -1)):
                problems.append("rationale says %s outranks %s but list "
                                "order is %s" % (hi, lo, " > ".join(order)))
    emit("C17-rbg-precedence-declared", not problems,
         "; ".join(problems) if problems
         else "RBG within-Activity precedence design content declared as "
              "[%s]" % " > ".join(order))


# ---------------------------------------------------------------- C18 (r6)

def check_c18(kep_text):
    """Round-6 fix guard (commits 667f7c71 + 39e2ecdf; also retires Copilot
    advisory C4495569567): target changes during initial RI creation fold
    into Create convergence — no Creating -> Updating / Recovering edge, no
    preUpdate / preRecover invocation — and `Updating` is the Activity
    reason only when no current blocker or incomplete observation takes
    precedence (C4495569567's suggested fix, verbatim at :438).
    Polarity: contract (fail == fold rule or blocker qualifier missing).
    Fails at 0b7285b7 (Creating->Updating edge present), passes at head.
    Note: F19 (C19) tracks the one leftover unscoped summary-prose sentence
    this contract deliberately does not cover.
    """
    problems = []
    # edge-table fold row (and no Creating->Updating edge)
    if not re.search(r"\| `Creating` \| An update or recovery target is "
                     r"identified before initial creation completes \| "
                     r"Remain `Creating`", kep_text):
        problems.append("edge table lacks the Creating fold row")
    if re.search(r"\| `Creating` \| A new update target is identified[^\n]*"
                 r"\| `Updating` \|", kep_text):
        problems.append("edge table still has a Creating->Updating edge")
    # situation-table fold row
    if not re.search(r"Remain Creating and converge toward the latest "
                     r"target; do not invoke preUpdate or preRecover",
                     kep_text):
        problems.append("situation-table fold row missing")
    # design prose: Creating exits only to Running/Terminating
    if not re.search(r"`Creating` exits only to `Running` or `Terminating`",
                     kep_text):
        problems.append("'Creating exits only to Running or Terminating' "
                        "statement missing")
    if not re.search(r"`Creating` does not transition directly to "
                     r"`Updating` or `Recovering`", kep_text):
        problems.append("'Creating does not transition directly' statement "
                        "missing")
    # C4495569567: Updating yields to live blockers
    if not re.search(r"`Updating` is the Activity reason only when no "
                     r"current blocker or incomplete observation takes "
                     r"precedence", kep_text):
        problems.append("Updating-yields-to-blocker qualifier missing")
    # test-plan fold bullet
    if not re.search(r"template changes during `Creating` remain `Creating`",
                     kep_text):
        problems.append("test-plan fold bullet missing")
    emit("C18-create-fold-consistency", not problems,
         "; ".join(problems) if problems
         else "Create-convergence fold stated consistently (edge table, "
              "situation table, prose, test plan) + Updating yields to "
              "blockers (C4495569567 retired)")


def check_c19(kep_text):
    """F18 repro (round-6, nit, revision-introduced by 667f7c71): the RI
    section's summary prose still says 'Identifying an update target enters
    Updating' / 'Confirming independent recovery ... enters Recovering'
    without phase scope, while the normative edge table now folds update /
    recovery targets identified during initial creation into Create
    convergence. An implementer coding from the prose alone would
    transition Creating -> Updating. Passes once the prose is scoped (e.g.
    'from Running / Recovering') or aligned with the fold rule.
    Polarity: contract (pass @0b7285b7 — prose matched the edge table there;
    fail @39e2ecdf — the delta changed the edge table but not the prose).
    """
    problems = []
    fold = re.search(r"Remain `Creating`; fold the new target into Create "
                     r"convergence", kep_text)
    for pat, what in (
            (r"Identifying an update target enters Updating[^.]*\.",
             "'Identifying an update target enters Updating'"),
            (r"Confirming independent recovery of required Pods outside an "
             r"update enters Recovering[^.]*\.",
             "'Confirming independent recovery ... enters Recovering'")):
        m = re.search(pat, kep_text)
        if m and fold:
            s = m.group(0)
            if not re.search(r"from `(?:Running|Recovering)`|once "
                             r"established|after initial creation "
                             r"completes|during initial creation", s):
                problems.append("unscoped RI summary prose contradicts the "
                                "Create-convergence fold: %s" % what)
    emit("C19-ri-prose-edge-consistency", not problems,
         "; ".join(problems) if problems
         else "RI summary prose scoped consistently with the edge table")


def check_c20(kep_text):
    """F14 repro (round-5 open, nit): the Consuming-the-Status decision
    table's needs-attention row lists configuration-compatibility families
    under `Progressing=True`, but every config-incompatibility reason maps
    to `Progressing=False` (:140 round-6 rule + :234 mapping) — a dead
    disjunct in the very table Flux CEL / Argo CD Lua implementers are told
    to codify. The `Accepted=False` disjunct already covers the family.
    Polarity: contract (fail == dead disjunct present). Fails at head;
    dropping 'or configuration-compatibility' from the Progressing=True
    list flips it green.
    """
    problems = []
    m = re.search(r"\| Does it need attention\? \|([^\n]*)\|", kep_text)
    if m is None:
        problems.append("decision-table needs-attention row not found")
    else:
        row = m.group(1)
        if "configuration-compatibility" in row and \
                "Progressing=True" in row:
            problems.append("needs-attention row lists configuration-"
                            "compatibility under Progressing=True (dead "
                            "disjunct: config-incompatibility reasons only "
                            "occur with Progressing=False)")
    emit("C20-consuming-table-attention-row", not problems,
         "; ".join(problems) if problems
         else "needs-attention row consistent with the "
              "Accepted=False -> Progressing=False mapping")


def check_c21(kep_text):
    """F16 repro (round-5 open, nit): the RI transition-timestamps migration
    row attributes the minReadySeconds lastTransitionTime dependency to
    stateless RIS only, but stateful mode's isInstanceAvailable
    (stateful_instance_set_helper.go:240-256, feeding availableReplicas at
    :219) also derives availability from RoleInstanceReady.
    lastTransitionTime — availability timing changes after upgrade in BOTH
    modes. Passes once the row says 'stateless and stateful'.
    Polarity: contract (fail == stateless-only scoping). Fails at head.
    """
    problems = []
    m = re.search(r"\| RI transition timestamps \|([^\n]*)\|", kep_text)
    if m is None:
        problems.append("RI transition timestamps migration row not found")
    else:
        row = m.group(1)
        if re.search(r"stateless RIS uses these timestamps", row) and \
                not re.search(r"stateful", row):
            problems.append("migration row scopes minReadySeconds to "
                            "stateless only; stateful isInstanceAvailable "
                            "reads the same lastTransitionTime")
    emit("C21-minready-both-modes", not problems,
         "; ".join(problems) if problems
         else "migration row covers both modes' minReadySeconds "
              "timestamp dependency")


def _consuming_section(kep_text):
    m = re.search(r"### Consuming the Status\n(.*?)\n### ", kep_text, re.S)
    return m.group(1) if m else ""


def check_c22(kep_text):
    """Q5 residual (round-5 open, nit, narrowed in round 6): the decision
    table states no kind scope. `Converged` is defined only in the RBG/RIS
    Progressing=False rows (the RI reason list is a hedged 'such as' list),
    and RIS adds no `Ready` condition — so a consumer applying the table to
    a RI (or its Ready row to a RIS) cannot answer the questions. Passes
    once the table is kind-scoped (RBG / RIS) or split per kind.
    Polarity: contract (fail == no kind scope). Fails at head.
    """
    problems = []
    sec = _consuming_section(kep_text)
    if not sec:
        problems.append("Consuming the Status section not found")
    elif not re.search(r"(?:for|applies to|scoped to)[^.]{0,40}"
                       r"`?RBG`?[^.]{0,10}`?RIS`?", sec):
        problems.append("decision table states no kind scope (Converged is "
                        "not an RI reason; RIS adds no Ready condition)")
    emit("C22-decision-table-kind-scope", not problems,
         "; ".join(problems) if problems
         else "decision table kind-scoped")


def check_c23(kep_text):
    """Round-6 fix guard (retires the mechanism half of Q5 / Copilot
    C4495569437): the completion row requires the `Progressing` condition's
    own observedGeneration == metadata.generation, because a legacy or
    rolled-back controller can advance status.observedGeneration while
    leaving a stale Converged condition written for an older generation.
    Polarity: contract (fail == condition-level check missing). Fails at
    0b7285b7, passes at 39e2ecdf.
    """
    problems = []
    m = re.search(r"\| Is the change done\? \|([^\n]*)\|", kep_text)
    if m is None:
        problems.append("decision-table completion row not found")
    else:
        row = m.group(1)
        if "condition's own `observedGeneration" not in row:
            problems.append("completion row lacks the Progressing "
                            "condition's own observedGeneration check")
    if not re.search(r"condition-level `observedGeneration` check is "
                     r"required for completion", kep_text):
        problems.append("condition-level observedGeneration rationale "
                        "paragraph missing")
    emit("C23-completion-observedGeneration", not problems,
         "; ".join(problems) if problems
         else "completion requires the condition's own observedGeneration "
              "(rollback-safe)")


def check_c24(kep_text):
    """Q6 repro (round-5 open, nit): nothing in the document states what
    `Progressing` reports while an RBG/RIS is terminating — the Terminating
    reasons were withdrawn from the reason tables, Ready is documented as
    not rewritten on deletion, and the current controller early-returns on
    deletionTimestamp, so Progressing goes stale with no documented
    expectation. Passes once one sentence ties Progressing semantics to
    deletion (e.g. 'Progressing retains its last value during deletion;
    consumers must check deletionTimestamp first').
    Polarity: contract (fail == no statement). Fails at head and at
    0b7285b7.
    """
    problems = []
    hit = False
    for line in kep_text.splitlines():
        s = line.strip()
        if s.startswith("|") or s.startswith("```"):
            continue  # skip table rows / fenced blocks
        for sent in re.split(r"(?<=\.)\s+", s):
            if "`Progressing`" in sent and re.search(
                    r"deletionTimestamp|deletion\b|Terminating|"
                    r"terminating", sent):
                hit = True
                break
        if hit:
            break
    if not hit:
        problems.append("no statement covers Progressing semantics during "
                        "deletion/termination")
    emit("C24-progressing-during-deletion", not problems,
         "; ".join(problems) if problems
         else "Progressing-during-deletion semantics stated")


def check_c25(kep_text):
    """F19 repro (round-6 late discovery, nit; dangling since the round-5
    pivot 0b7285b7): :247 instructs 'select the RI phase using the display
    priority defined in the RoleInstance section', but the RoleInstance
    section defines no display priority (the multi-workflow selection rules
    exist in the edge / situation tables but are never labeled 'display
    priority'). Passes once the reference resolves or is reworded.
    Polarity: contract (fail == dangling reference). Fails at head and at
    0b7285b7.
    """
    problems = []
    if re.search(r"display priority defined in the RoleInstance section",
                 kep_text):
        m = re.search(r"### RoleInstance\n(.*?)\n### Test Plan", kep_text,
                      re.S)
        ri = m.group(1) if m else ""
        if "display priority" not in ri:
            problems.append("reference to 'display priority defined in the "
                            "RoleInstance section' does not resolve — the "
                            "RoleInstance section defines no display "
                            "priority")
    emit("C25-display-priority-resolves", not problems,
         "; ".join(problems) if problems
         else "display-priority reference resolves within the RoleInstance "
              "section")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", default=".")
    ap.add_argument("--kep",
                    default="keps/502-workload-status-model/README.md")
    args = ap.parse_args()
    repo = os.path.abspath(args.repo)
    kep_path = os.path.abspath(args.kep)
    if not os.path.exists(kep_path):
        sys.stderr.write("kep not found: %s\n" % kep_path)
        print("C0-harness\tnot-run")
        return 1
    kep_text = read(kep_path)
    yaml_dir = os.path.dirname(kep_path)

    check_c1(kep_text, repo)
    check_c2(kep_text)
    check_c3(kep_text)
    check_c4(kep_text, repo)
    check_c5(repo)
    check_c6(kep_path, yaml_dir)
    check_c8(kep_text)
    check_c9(kep_text)
    check_c10(kep_text)
    check_c11(kep_text, repo)
    check_c12(kep_text)
    check_c13(kep_text)
    check_c14(kep_text)
    check_c15(kep_text)
    check_c16(kep_text)
    check_c17(kep_text)
    check_c18(kep_text)
    check_c19(kep_text)
    check_c20(kep_text)
    check_c21(kep_text)
    check_c22(kep_text)
    check_c23(kep_text)
    check_c24(kep_text)
    check_c25(kep_text)

    for cid, ok, detail in RESULTS:
        print("%s\t%s" % (cid, "pass" if ok else "fail"))
        sys.stderr.write("  [%s] %s: %s\n" % (cid, "PASS" if ok else "FAIL",
                                              detail))
    return 0


if __name__ == "__main__":
    sys.exit(main())
