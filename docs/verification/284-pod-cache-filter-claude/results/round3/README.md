Round 3 raw outputs, two runs:

- `./` (run 1, before the canary inversion): the round-2 bug-canary
  `TestMedianRangeUpperMiddleForEvenGroups` FAILS in both gate configs —
  the R2-F4 fix (medianRange averages even groups) flipped it exactly as
  designed. See `reverify-summary-flip.txt` and `integration.out`.
- `../round3-after-inversion/` (run 2, after inverting the canary into a
  contract): everything green except the by-design reds (P0 premise, F1 x2,
  R2-F2). See its `reverify-summary.txt`.
