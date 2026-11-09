import subprocess
SRC = "/tmp/kep-39e2ecdf.md"
CHECK = "docs/verification/502-workload-status-model-claude/scripts/check_kep502.py"
def run(text):
    with open("/tmp/bites6/mut.md","w") as f: f.write(text)
    out = subprocess.run(["python3","-I",CHECK,"--repo",".","--kep","/tmp/bites6/mut.md"],
                         capture_output=True, text=True)
    return dict(l.split("\t") for l in out.stdout.strip().splitlines() if "\t" in l)
base = open(SRC).read()
def mut(name, old, new, expect):
    assert old in base, "anchor missing for %s" % name
    r = run(base.replace(old, new, 1))
    print("%-32s %s" % (name, {k: r.get(k,"?") for k in expect}))

# fixA redone: scope BOTH RI summary-prose sentences (the realistic F18 fix)
mut("fixA-F18-scope-both-sentences",
    "Deletion of the RI takes priority and enters Terminating. Identifying an update target enters Updating; an empty revision during initial creation is not evidence of an update.\nConfirming independent recovery of required Pods outside an update enters Recovering.",
    "Deletion of the RI takes priority and enters Terminating. From `Running` or `Recovering`, identifying an update target enters Updating; an empty revision during initial creation is not evidence of an update.\nFrom `Running`, confirming independent recovery of required Pods outside an update enters Recovering.",
    ["C19-ri-prose-edge-consistency"])
# mut6 redone: restore the round-5 contradiction in the :510 test-plan bullet
mut("mut6-C15-bites (restore :510 contradiction)",
    "including preCheck failures, the parent writes `observedGeneration=N` together with the `Progressing` value derived from validation, pause, and progress rules",
    "including preCheck failures, the parent writes `observedGeneration=N` together with `Progressing=True`",
    ["C15-precheck-write-consistency"])
