# Design principles and roadmap

## Principles

- **The durable assets are the idempotency map and the append-only audit log — not the mirror.** They live in a separate, backed-up store; the mirror is disposable. Structure compounds; a read cache doesn't.
- **Idempotent where it books.** Fiken has no idempotency key, so the CLI owns one. `commit` checks a durable source→entity map first, so re-running the same bank line or document is a no-op rather than a duplicate posting. `reconcile` carries no such map yet: a repeated payment registers twice.
- **Audited in the three commands that book.** `commit`, `reconcile` and `reverse` append to the log in the same call that writes, recording source, rationale, confidence, the Fiken request id and the result. The append is a separate statement, not one transaction with the remote call, and its error is swallowed: a successful posting can leave no event behind. The generated create/update/delete commands never touch the log at all, so an agent that uses them calls `log-event` itself. The CLI and the browser reconciliation harness feed that same single log.
- **Agent-native.** Composable commands, an offline searchable mirror, compact/JSON output, built to be called thousands of times a day and driven from Claude Code.
- **Reconciliation-first.** The bank-line workflow is the point; everything else serves it.

## Roadmap

Known gaps, stated so nothing above reads as a guarantee it is not:

- **Idempotent payments.** `reconcile` consults no source→entity map, so a repeated payment registers twice. It needs a durable key and a reserve/advance lifecycle like `commit`'s.
- **Payments in foreign currency.** `reconcile` sends no `amountInNok`, which Fiken requires whenever the amount is not in NOK, so those payments go through `purchases payments create-purchase` instead.
- **Payment-processor clearing.** Payout and card-settlement flows still need their clearing-account journal entries posted by hand; no command composes them.
- **Crash-safe writes.** A crash between a successful POST and the idempotency advance leaves the row reserved rather than committed, so a retry re-posts and double-books. Closing it needs an outbox or a recovery pass, and the swallowed audit error surfaced.
- **Audit coverage of the generated commands.** The ~70 generated create/update/delete commands write to Fiken with no local event. Either route them through the audit sink or document each as agent-audited.
- **Verified mirror coverage.** Twelve resources sync out of 153 operations in `spec.yaml`. The set deserves a coverage report rather than a claim.
