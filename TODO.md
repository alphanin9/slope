# TODO

## Scheduler Concurrency

The current scheduler is intentionally simple and is safest as a single service process with one worker loop.

Known limitations:

- Tasks execute one at a time globally, even when multiple Windows VM profiles are configured.
- The SQLite connection is limited to one open connection, which serializes in-process DB access but is not a distributed scheduler lock.
- `ClaimNextTask` uses a transaction around task claim and machine lock, but it has not been hardened for multiple service processes sharing the same SQLite DB.
- Screenshot sequence allocation uses `MAX(seq)+1`, which assumes only one worker captures screenshots for a given task.
- Machine selection requires the submitted task to name one VM; there is no pool-level "choose any compatible free Windows VM" scheduler yet.

Future hardening direction:

- Use SQLite `BEGIN IMMEDIATE` or atomic `UPDATE ... RETURNING` claim operations for stronger multi-worker behavior.
- Add configurable worker concurrency, ideally bounded by machine count.
- Support machine pools so a task can request `platform=windows` rather than one exact VM name.
- Keep screenshot sequence generation task-owner-only, or move it behind a DB-side atomic counter if parallel capture is introduced.
- If multiple service processes are needed, prefer Postgres with row-level locks such as `FOR UPDATE SKIP LOCKED`.
