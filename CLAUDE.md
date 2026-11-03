# libre2sim: instructions for Claude sessions

**Before doing anything, read `HANDOFF.md`.** It holds the project status, verified findings, environment
gotchas, open decisions waiting on the user, and how to recreate the scratch setup.

**Keep `HANDOFF.md` current, continuously.** Update it and commit it (`git add HANDOFF.md && git commit`):

- when you start a task, or delegate one to a subagent
- when a subagent reports back
- after each verification step, with the result and anything surprising added to its "Verified findings"
- before you stop, including adding a row to its "Session log"

The user requires this so that no work or findings are lost if a session ends suddenly (e.g. usage limits).

Other rules (details in `HANDOFF.md` §5 and §10):

- Run Go with `GO111MODULE=on` (it's off globally here). The toolchain is Go 1.20.
- Verify every subagent's result yourself before committing, using LibreTransmitter's real Swift via `tools/*harness`.
- Subagents must not edit `HANDOFF.md` or commit.
- Don't post to GitHub or push anywhere without the user's explicit OK.
