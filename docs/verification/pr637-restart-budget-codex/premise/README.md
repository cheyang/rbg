# Premise (P0) assets

- `restart_budget_premise_verify_test.go` — premise tests for the **base** branch. They compile
  only against base (`45e6e1c2`), where `reconcileRestartBudget` has the pre-PR signature.
  Run: `git checkout 45e6e1c2`, copy this file into `pkg/reconciler/roleinstance/sync/`, then
  `go test -mod=vendor ./pkg/reconciler/roleinstance/sync/ -run TestVerifyP0 -count=1 -v`.
  Both PASS on base by demonstrating the bug (that is the premise evidence).
- `migration-fix.patch` — the smallest production change that makes the F1a/F1b contract tests on
  this branch pass (`git apply` on the PR head). Evidence: `../results/f1-with-migration-fix.txt`.
  Not applied here; production code on this branch is untouched.
