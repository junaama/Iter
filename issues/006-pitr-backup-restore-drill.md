## Parent PRD

`ARCHITECTURE.md` §9 Step 1 — Data model; §7 reliability ("postgres-down" runbook in Step 7). `deploy.md` for the Railway-centric ops context.

## What to build

Execute a point-in-time-recovery (PITR) drill against the Railway Postgres instance. The drill simulates a destructive accident and restores the database to a known-good moment, end-to-end:

1. Confirm PITR is enabled on the Railway Postgres plan in use (paid plans only — verify before assuming).
2. Insert a recognizable "before" row with a known timestamp.
3. Wait long enough for the row to be captured by WAL archiving / continuous backup.
4. Execute a destructive action (`TRUNCATE` a non-production table, or `DROP` a sample table).
5. Use Railway's PITR mechanism (or `pg_restore` from the latest backup, whichever Railway exposes) to restore to the timestamp just before the destruction.
6. Confirm the recognizable row is back and the destructive action has been undone.
7. Record the wall-clock time the restore took.

Document the full procedure in `deploy.md` (or a new `runbooks/postgres-restore.md`) so on-call can repeat it under stress.

## Acceptance criteria

- [ ] PITR / continuous backup confirmed enabled on the Railway Postgres plan
- [ ] Drill executed against the dev (or staging) instance — NEVER prod
- [ ] Recognizable "before" row recovered successfully
- [ ] Restore wall-clock time recorded
- [ ] Step-by-step runbook committed (`deploy.md` section or `runbooks/postgres-restore.md`)
- [ ] Runbook includes: where to find the Railway restore UI / API, retention window, how to identify the correct restore timestamp, and a rollback plan if the restore itself fails
- [ ] `DECISIONS.md` updated if the drill surfaces any architectural constraint (e.g. retention window shorter than the 90-day hot-storage SLA implies)

## Blocked by

- Blocked by `issues/001-provision-postgres-railway.md`

## User stories addressed

Reliability invariant; not user-facing but required for the §7 "postgres-down" failure mode to be answerable.
