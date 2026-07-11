# GH-398-A authority/evidence manifest

Work item: [GH-421](https://github.com/agent-team-project/kensho/issues/421)

Parent: [GH-398](https://github.com/agent-team-project/kensho/issues/398)

Frozen protocol SHA-256: `27646995428fa592f1d86d97c17a6336ecb7f91d597b1f17ad7403fb820645eb`

Reconciled base: `9311b3d478edc0e539caa52d06db5c6ca29ae3fa`

## Acceptance-node evidence

| Node | Implementation evidence | Deterministic evidence |
| --- | --- | --- |
| `R0` | `query_authenticated_pr_head` obtains PR URL, full head SHA, authenticated viewer, timestamp, transport, and closed status in one authenticated GraphQL response. The resolution and write queries are persisted in the verifier JSON and digest-bound exact-head attestation. | `VerifyExactHeadTest.test_fixture_a_authoritative_head_wins_all_local_ref_shapes`; `VerifyExactHeadTest.test_attestation_schema_rejects_invalid_closed_vocabulary_combinations` |
| `R1` | `resolve_commit` enters the PR-authoritative path first; `fetch_authoritative_pr_head` validates `origin` identity, fetches `refs/pull/<n>/head`, and compares it with the oracle before any explicit/local/worktree fallback. | Fixture A table covers stale, divergent, local-ahead/unpushed, missing-local, and explicit-stale commit. `test_fixture_a_missing_origin_fails_closed_without_local_fallback` and `test_unknown_resolution_query_fails_closed_before_local_lookup` cover unavailable shapes. |
| `R2` | `exact_head_decision` re-queries after gates and requires checkout, evidence, and GitHub full SHAs to match before paired evidence/attestation publication and successful step completion. The attestation binds the canonical verifier JSON by SHA-256. | Fixture B (`test_fixture_b_head_advance_blocks_green_evidence_and_successful_completion`) advances the PR ref at a gate barrier and observes `exact_head_mismatch`, `class: infra`, failure evidence, and only failed (never successful) completion. |
| `R4` | Closed query/equality/disposition/reason/review-phase vocabularies are validated before publication. Unknown, malformed, missing-origin, and unequal rows use `block_infra`; only authenticated equality uses `dispatch`. | `test_unknown_write_query_fails_closed_instead_of_using_resolution_cache`; the schema invalid-combination table; Fixture A missing/unavailable rows; Fixture B mismatch row. |

The ordinary authenticated equal-head control remains green and calls step
completion with `status=pass`. Non-PR jobs retain the pre-existing resolution
path and evidence behavior.

## Load-bearing mutants

The executable mutation manifest is
`scripts/skills/verify/test_exact_head_mutations.py`. Each command copies the
verifier to a temporary module, applies exactly the unified diff printed by
`--show-diff`, and succeeds only when the named unmodified focused test fails:

| Mutant | Exact command | Required killing test | Result |
| --- | --- | --- | --- |
| `1` — restore local-first resolution | `PYTHONDONTWRITEBYTECODE=1 python3 -W error::ResourceWarning scripts/skills/verify/test_exact_head_mutations.py --mutant 1 --show-diff` | `VerifyExactHeadTest.test_fixture_a_authoritative_head_wins_all_local_ref_shapes` | `KILLED` |
| `2` — downgrade the evidence-write equality assertion | `PYTHONDONTWRITEBYTECODE=1 python3 -W error::ResourceWarning scripts/skills/verify/test_exact_head_mutations.py --mutant 2 --show-diff` | `VerifyExactHeadTest.test_fixture_b_head_advance_blocks_green_evidence_and_successful_completion` | `KILLED` |
| `4` — reuse cached resolution identity when the fresh query is unavailable | `PYTHONDONTWRITEBYTECODE=1 python3 -W error::ResourceWarning scripts/skills/verify/test_exact_head_mutations.py --mutant 4 --show-diff` | `VerifyExactHeadTest.test_unknown_write_query_fails_closed_instead_of_using_resolution_cache` | `KILLED` |

All three together:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -W error::ResourceWarning scripts/skills/verify/test_exact_head_mutations.py --show-diff
```

## Scope boundary

This slice changes only verifier authority, resolution, evidence-write
attestation, tests, and verifier documentation. It does not implement daemon
pre-review dispatch (`R3`), reviewer-start coupling (`R5`), recurrence/audit
machinery (`R7`), bounce/reset behavior, or follow-on GH-398 slices.
