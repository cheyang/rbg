# Verification — PR #390 "doc: add doc best-practice/stress-testing-and-tuning"

Reviewer: Codex (Reviewer B). Branch: `verify/stress-testing-docs-codex`.
Base code under review: PR head `b38e9d8236d4f2481c16a7a445d57d72eaac8ee1`.
**Production/doc code untouched — this branch adds only `docs/verification/**`.**

## Premise (P0)

Docs-only PR; premise validation is carved out per the review rubric. The implicit
premise — "the repo lacks operator-facing stress-testing/tuning docs" — holds on
`origin/main` (no `09-stress-*` files under `doc/best-practice/`). Everything the
docs reference exists at the PR head (`test/stress/`, `deploy/helm/rbgs`,
`.claude/skills/stress-test/`). Component match: yes.

Because the PR is docs, the findings are **factual-accuracy defects**: statements in
the new docs that contradict the repository they describe.

## Hypotheses and verdicts

| ID | Claim | Layer | Polarity | Verdict |
|----|-------|-------|----------|---------|
| F1 | Docs say k8s `>= 1.24`; `doc/install.md` requires `>= 1.28` | unit (static) | contract | **Reproduced** |
| F2 | Docs say go `>= 1.22`; root `go.mod` (governs `go run ./test/stress/`) says `go 1.25.0` | unit (static) | contract | **Reproduced** |
| F3 | Docs claim "~1.5 s from creation to Ready"; `pod-running` stage already sets `Ready=True` at ~500 ms and `pod-ready`'s selector (`Ready NotIn True`) is then unreachable | unit (static) | contract | **Reproduced** |
| F4 | Hypothesis: `kubectl -o jsonpath='{...args}' \| tr ',' '\n'` is a no-op (space-separated list) | unit (empirical, kubectl dry-run) | contract | **Refuted** — kubectl JSON-marshals single slice results, so `tr` splits exactly into the doc's shown format |
| F5 | Guide summary says "see Operation 5 note" / "见操作五说明" for the Update regression, but Operation 5 contains no such note (en + zh) | unit (static) | contract | **Reproduced** |
| F6 | Docs call `/stress-test` a "Claude Code built-in Skill"; it actually ships in-repo at `.claude/skills/stress-test/` | unit (static) | contract | **Reproduced** |
| F7 | Guide's expected controller-args block omits `--enable-deprecated-workload-types=true`, which the chart renders by default | integration (`helm template` with the doc's exact `--set` flags) | contract | **Reproduced** |

## How to run

```bash
# Individual checks (from repo root):
bash docs/verification/stress-testing-docs/checks/f1-k8s-version.sh   # F1
bash docs/verification/stress-testing-docs/checks/f2-go-version.sh    # F2
bash docs/verification/stress-testing-docs/checks/f3-kwok-ready-timing.sh  # F3
bash docs/verification/stress-testing-docs/checks/f4-args-tr.sh       # F4 (refuted; stays green)
bash docs/verification/stress-testing-docs/checks/f5-operation5-note.sh    # F5
bash docs/verification/stress-testing-docs/checks/f6-skill-builtin.sh # F6
bash docs/verification/stress-testing-docs/checks/f7-helm-args.sh     # F7 (needs helm)
bash docs/verification/stress-testing-docs/checks/l2-markdownlint.sh  # docs-check.yml parity (needs npx)

# Full re-verify against the current PR head (fetches it automatically):
bash docs/verification/stress-testing-docs/scripts/re-verify.sh
```

Exit code 1 from a contract check = defect still present on the checked-out code.

## Observed vs expected

| Check | Expected (contract) | Observed on PR head | After applying proposed fix |
|-------|--------------------|---------------------|----------------------------|
| f1 | version floors match install.md | `1.24` vs `1.28` in all 4 docs | green (`results/harness-bites-f1.txt`) |
| f2 | go floor satisfies go.mod | `1.22` vs `1.25.0` in all 4 docs | green (`results/harness-bites-f2.txt`) |
| f3 | doc timing matches kwok-stage.yaml | 1.5 s claimed; ~500 ms actual; guide's own sample shows min create 1024 ms < 1500 ms | green (`results/harness-bites-f3.txt`) |
| f4 | doc's tr pipeline reproduces shown output | it does (kubectl v1.37) | n/a — refuted (`results/f4-args-tr.txt`) |
| f5 | referenced note exists | no regression note in Operation 5 (en+zh) | green (`results/harness-bites-f5.txt`) |
| f6 | skill not described as built-in | "Claude Code's built-in" / "Claude Code 内置" in both concept docs | green (`results/harness-bites-f6.txt`) |
| f7 | rendered args ⊆ doc's expected block | `--enable-deprecated-workload-types=true` missing | green (`results/harness-bites-f7.txt`) |
| l2 | markdownlint clean (repo config) | clean, 0 issues; lint proven to bite via injected-violation file | n/a |

Positive results also verified (no findings): all 16 stress-client CLI flags and
defaults match `test/stress/main.go`; all Helm values keys used by the docs
(`controller.tuning.*`, `controller.pprof.*`, `controller.resources.*`,
`controller.image.tag`) exist and render; deployment name/label/namespace match;
`setup-kwok.sh` / `teardown-kwok.sh` behavior matches the docs (stage-fast +
custom stages, `pod-complete` deleted, 6 stages); output file set matches
`pprof.go`/`report.go`/`timing.go`; guide's 600-Pod count matches
`templates.go` (100 × (4+1+1)); `summary.json` schema matches `PhaseStats`; the
sample arithmetic (gaps, P50/P99) is internally consistent.

## Proposed fixes

- F1: change `>= 1.24` → `>= 1.28` (all 4 files) to match `doc/install.md`.
- F2: change `go >= 1.22` → `go >= 1.25` (all 4 files) to match `go.mod`.
- F3: drop the pod-ready 1000 ms hop from the "time to Ready" narrative
  (`pod-running` already sets `Ready=True`); state ~500 ms, or remove the
  redundant `pod-ready` stage from `kwok-stage.yaml` in a follow-up code change.
- F5: add the missing Update-regression note to Operation 5 (en + zh), or drop
  the parenthetical from the summary table.
- F6: describe `/stress-test` as a skill shipped in this repository
  (`.claude/skills/stress-test/`), not "built into" Claude Code.
- F7: add `--enable-deprecated-workload-types=true` to the expected-args block
  (guide, en; zh block likewise if present).

## Continuing after the fix

1. `git fetch https://github.com/cheyang/rbg.git verify/stress-testing-docs-codex && git checkout verify/stress-testing-docs-codex`
2. `bash docs/verification/stress-testing-docs/scripts/re-verify.sh` — fetches the
   current PR head, grafts this harness onto it, and prints Fixed/Still-present
   per finding (all findings are contract polarity: green = fixed).
3. Review the delta printed by re-verify (`.last-reviewed..head`), then advance:
   `echo <head> > docs/verification/stress-testing-docs/.last-reviewed`, commit, push.

Prerequisites: bash, git, grep/awk/sed; `helm` for F7; `kubectl` for F4;
`npx` for the markdownlint parity check. No cluster required (no live layer).
