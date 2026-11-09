import subprocess, sys, os
SRC = "/tmp/kep-39e2ecdf.md"
CHECK = "docs/verification/502-workload-status-model-claude/scripts/check_kep502.py"
def run(text):
    with open("/tmp/bites6/mut.md","w") as f: f.write(text)
    out = subprocess.run(["python3","-I",CHECK,"--repo",".","--kep","/tmp/bites6/mut.md"],
                         capture_output=True, text=True)
    return dict(l.split("\t") for l in out.stdout.strip().splitlines() if "\t" in l)
base = open(SRC).read()
r0 = run(base)
def mut(name, old, new, expect):
    assert old in base, "anchor missing for %s" % name
    r = run(base.replace(old, new, 1))
    got = {k: r.get(k,"?") for k in expect}
    print("%-28s %s" % (name, got))
    return r

# --- proposed-fix mutations (open findings -> guard flips green) ---
mut("fixA-F18-scope-prose",
    "Identifying an update target enters Updating;",
    "Identifying an update target from `Running` or `Recovering` enters Updating;",
    ["C19-ri-prose-edge-consistency"])
mut("fixB-F14-drop-disjunct",
    "quota, resources, gang, dependencies, or configuration-compatibility families",
    "quota, resources, gang, dependencies, or permission families",
    ["C20-consuming-table-attention-row"])
mut("fixC-F13-restore-bullet",
    "When one role is `Recovering` and another is updating, RBG uses `Recovering`.",
    "When one role is `Creating` and another is updating or recovering, RBG uses `Creating`. When one role is `Recovering` and another is updating, RBG uses `Recovering`.",
    ["C10-rbg-activity-precedence"])
mut("fixD-F16-both-modes",
    "stateless RIS uses these timestamps for `minReadySeconds`",
    "stateless and stateful RIS use these timestamps for `minReadySeconds`",
    ["C21-minready-both-modes"])
mut("fixE-Q5-kind-scope",
    "Automation should use a small, stable decision table:",
    "Automation should use a small, stable decision table for RBG and RIS:",
    ["C22-decision-table-kind-scope"])
mut("fixF-Q6-deletion-sentence",
    "A deletionTimestamp does not by itself rewrite Ready.\n",
    "A deletionTimestamp does not by itself rewrite Ready. `Progressing` retains its last value during deletion; consumers must check `metadata.deletionTimestamp` first.\n",
    ["C24-progressing-during-deletion"])
mut("fixG-F19-define-display-priority",
    "No phase returns to `Creating`, and `Creating` exits only to `Running` or `Terminating`:",
    "Phase display priority while multiple workflows remain incomplete is `Updating` over `Recovering` (see the situation table). No phase returns to `Creating`, and `Creating` exits only to `Running` or `Terminating`:",
    ["C25-display-priority-resolves"])

# --- regression mutations (fixed guards -> flip red: the guards bite) ---
mut("mut1-C13-bites (drop RBG scoping)",
    "`Creating` is used only during initial RBG establishment, before the target's required roles have been established. Once the RBG is established, a missing child is not `Creating`: it is classified under the workflow that requires it, such as `Recovering` for replacing an established child or `ScalingOrUpdating` for a target that adds one. ",
    "",
    ["C13-replacement-mapping-stated","C16-replacement-discriminator"])
mut("mut2-C16-bites (drop RIS discriminator)",
    "before the RIS has an established revision or established RIs for its target",
    "at some point",
    ["C16-replacement-discriminator"])
mut("mut3-C18-bites (restore old edge)",
    "| `Creating` | An update or recovery target is identified before initial creation completes | Remain `Creating`; fold the new target into Create convergence without invoking hooks |",
    "| `Creating` | A new update target is identified before initial creation completes | `Updating` |",
    ["C18-create-fold-consistency"])
mut("mut4-C23-bites (drop condition-observedGeneration)",
    ", and the `Progressing` condition's own `observedGeneration == metadata.generation` |",
    " |",
    ["C23-completion-observedGeneration"])
mut("mut5-C11-bites (drop KEP-527 delegation)",
    "Hook execution, retries, and release use the hook-state labels and finalizer handshake defined by KEP-527.",
    "Hook execution, retries, and release use a label protocol.",
    ["C11-round5-code-claims"])
mut("mut6-C15-bites (restore round-5 contradiction)",
    "together with the `Progressing` value derived from the validation, pause, and progress rules before or while dispatching that target to children. Incomplete work writes `Progressing=True`; a confirmed compatibility failure writes `Progressing=False` with the matching incompatibility reason; a full pause writes `Progressing=False(reason=Paused)`.",
    "together with `Progressing=True`.",
    ["C15-precheck-write-consistency"])
