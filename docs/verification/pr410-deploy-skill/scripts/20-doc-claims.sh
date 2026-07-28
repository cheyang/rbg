#!/usr/bin/env bash
# L1-mechanical checks of the factual claims CLAUDE.md / SKILL.md make about this repo.
# No cluster needed. Run from the repo root. Polarity: CONTRACT (each check asserts the
# claim is TRUE; a FAIL line means the doc added by PR #410 is wrong).
set -uo pipefail
fail=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fail=$((fail+1)); }

echo "=== F2 — CLAUDE.md:13-15 documents make targets for the CLI ==="
for t in build build-cli build-cli-all; do
  if grep -qE "^${t}:" Makefile; then ok "Makefile target '$t' exists"
  else bad "Makefile has no target '$t' (CLAUDE.md documents 'make $t')"; fi
done
echo "--- does ANY make target build cmd/cli? ---"
if grep -nE "^[a-zA-Z0-9_.-]+:" -A6 Makefile | grep -q "cmd/cli"; then
  ok "some target references cmd/cli"
else
  bad "no Makefile target references cmd/cli at all — the CLI has no documented build path"
fi

echo
echo "=== F3 — CLAUDE.md:130-135 documents 'kubectl rbg llm ...' subcommands ==="
if grep -rq "NewLLMCmd" cmd/cli/cmd/root.go; then
  ok "llm command tree is registered on the CLI root"
else
  bad "NewLLMCmd is never registered in cmd/cli/cmd/root.go — 'kubectl rbg llm' does not exist"
fi
echo "--- build the CLI and ask it what it supports ---"
if go build -o /tmp/kubectl-rbg-verify ./cmd/cli 2>/tmp/cli-build.err; then
  echo "built ok; top-level commands reported by the binary:"
  /tmp/kubectl-rbg-verify --help 2>&1 | sed -n '/Available Commands/,/^$/p'
  for sub in llm; do
    if /tmp/kubectl-rbg-verify --help 2>&1 | grep -qE "^\s+${sub}\b"; then
      ok "'$sub' is an available command"
    else
      bad "'$sub' is NOT an available command, so all 5 'kubectl rbg llm ...' lines in CLAUDE.md are unusable"
    fi
  done
  echo "--- direct invocation ---"
  /tmp/kubectl-rbg-verify llm --help 2>&1 | head -3
else
  bad "CLI failed to build: $(head -3 /tmp/cli-build.err)"
fi

echo
echo "=== F4 — CLAUDE.md:120-123 lists restartPolicy values ==="
echo "--- Go constants ---"
grep -rn "RestartPolicyType = \"" api/workloads/v1alpha2/rolebasedgroup_types.go || true
echo "--- kubebuilder enum actually generated ---"
grep -rn "Enum={None" api/workloads/v1alpha2/rolebasedgroup_types.go || true
if grep -rq "Enum={None,RecreateRBGOnPodRestart" api/workloads/v1alpha2/rolebasedgroup_types.go; then
  ok "RecreateRBGOnPodRestart is an accepted enum value"
else
  bad "RecreateRBGOnPodRestart is NOT in the restartPolicy enum, yet CLAUDE.md:122 documents it as valid"
fi
echo "--- where does v1alpha2 put restartPolicy? ---"
grep -n "RestartPolicy RestartPolicyType" -B12 api/workloads/v1alpha2/rolebasedgroup_types.go \
  | grep -E "^[0-9]+[-:]type |RestartPolicy RestartPolicyType" || true

echo
echo "=== F5 — SKILL.md fenced-code-block balance ==="
python3 - <<'PY'
import sys
p=".claude/skills/rbg-inference-deploy/SKILL.md"
lines=open(p,encoding="utf-8").read().split("\n")
fences=[i+1 for i,l in enumerate(lines) if l.strip().startswith("```")]
print("fence lines:", fences)
print("fence count:", len(fences), "(odd count => unbalanced)")
inside=False; spans=[]; start=None
for i,l in enumerate(lines,1):
    if l.strip().startswith("```"):
        if not inside: inside=True; start=i
        else: inside=False; spans.append((start,i))
print("unterminated fence at EOF:", inside)
if len(fences)%2: print("ROOT CAUSE: odd fence count. The summary box at lines 197-215 carries "
                 "THREE fences (197, 200, 215) instead of two; the stray one at 200 inverts "
                 "fence parity for the whole rest of the file.")
# A fence span that swallows markdown headings is almost certainly a mistake.
# Only markdown headings (## / ###) count; a single "#" is usually a shell comment
# inside a legitimate bash fence, so it would be a false positive.
def is_heading(l): return l.startswith("## ") or l.startswith("### ")
bad=[(a,b) for a,b in spans if any(is_heading(lines[k-1]) for k in range(a+1,b))]
for a,b in bad:
    heads=[(k,lines[k-1]) for k in range(a+1,b) if is_heading(lines[k-1])]
    print("FAIL  fence %d..%d renders %d markdown heading(s) as code:" % (a,b,len(heads)))
    for k,h in heads[:6]: print("        line %d: %s" % (k,h[:70]))
sys.exit(1 if bad or inside else 0)
PY
[ $? -ne 0 ] && bad "SKILL.md has a malformed code fence (see above)" || ok "SKILL.md fences are balanced and enclose no headings"


echo
echo "=== F5 cross-check with a real markdown parser (marked), if available ==="
if [ -d /tmp/node_modules/marked ] || npm --prefix /tmp ls marked >/dev/null 2>&1; then
  node -e '
const {marked}=require("/tmp/node_modules/marked");
const src=require("fs").readFileSync(".claude/skills/rbg-inference-deploy/SKILL.md","utf8");
const heads=marked.lexer(src).filter(t=>t.type==="heading").map(t=>t.text);
// Headings the author clearly intended (they exist as "## ..." lines in the source)
const intended=src.split("\n").filter(l=>/^#{2,3} /.test(l)).map(l=>l.replace(/^#+ /,""));
const lost=intended.filter(h=>!heads.some(x=>x.trim()===h.trim()));
console.log("headings written in source:", intended.length);
console.log("headings a renderer actually sees:", heads.length);
if(lost.length){
  console.log("FAIL  "+lost.length+" section heading(s) are swallowed into a code block and never render:");
  lost.forEach(h=>console.log("        "+h));
  process.exit(1);
}
console.log("PASS  every source heading renders as a heading");
' || bad "marked confirms SKILL.md sections are swallowed by a code block"
else
  echo "SKIP  marked not installed (npm --prefix /tmp install marked); the fence-parity check above is authoritative"
fi

echo
echo "=== final summary: $fail failing claim(s) ==="
exit $([ $fail -eq 0 ] && echo 0 || echo 1)
