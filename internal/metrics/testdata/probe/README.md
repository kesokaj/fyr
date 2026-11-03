# Antigravity 1.4.12 timing probe fixture

Recorded on macOS (`agy 1.4.12`) on 2026-11-03 against a scratch home, with `step_payload` and prose fields (`steps.metadata` text and `gen_metadata` prompt snapshots) stripped and `workspace_uris` replaced with `file:///workspace/probe`. The protobuf timestamp and status-history bytes (`1`, `6`, `7`, `8`, `26` in `steps.metadata` and `1.4`, `1.9`, `1.11`, `1.12`, `1.19` in `gen_metadata.data`) are verbatim from the live SQLite databases.

## Conversations

1. `6a0066be-3958-4af9-9319-a4544694a20f` — interactive `agy -i` session (`--mode accept-edits`, `--allow-cmd 'sleep *'`):
   - `idx=1,3,5,7,9,11` (`step_type = 15`, assistant reply): `t1` (created) < `t6` (equals status `8` streaming start in `26`) < `t7 == t8` (equals status `3` completed in `26`). Total model time across the 6 replies is `35,253 ms` (H3).
   - `idx=2` (`step_type = 132`, `run_command` `sleep 10`, pre-approved by `--allow-cmd 'sleep *'`): `t1 = 1793730638049`, `t6 = t7 = 1793730638268`, `t8 = 1793730648316`, `t8 - t6 = 10,048 ms` (wall clock 10.0 s), status history `[(1, 1793730638268), (2, 1793730638278), (3, 1793730648316)]`, `wait_ms = 0` (H1).
   - `idx=6` (`step_type = 132`, `run_command` `echo DIALOG_APPROVED`, answered after a counted `5.12 s` PTY pause at the permission dialog): `t1 = 1793730664085`, `t6 = t7 = 1793730664234`, `t8 = 1793730669412` (`t8 - t6 = 5,178 ms`), status history `[(1, 1793730664235), (2, 1793730664240), (9, 1793730664242), (2, 1793730669387), (3, 1793730669412)]`. Status `9` (`WAITING`) spans `1793730669387 - 1793730664242 = 5,145 ms`; tool execution is `(t8 - t6) - wait_ms = 33 ms` (H2).
   - `idx=8` (`step_type = 132`, `invoke_subagent` `research`): `t6 = t7 = 1793730675697`, `t8 = 1793730675736` (`39 ms`). While the child conversation runs (`1793730675748`..`1793730684968`), the parent finishes `idx=9` at `1793730680634` and wakes at `idx=11` (`1793730684993`), leaving a `4,359 ms` uncovered gap (`5,229 ms` total idle across the main session).
2. `c3882232-59f6-4709-b3cf-f7feec3e998e` — the `research` subagent of `6a0066be` (`parent_conversation_id = '6a0066be-3958-4af9-9319-a4544694a20f'`):
   - `idx=1` (`step_type = 15`): `t1 = 1793730675751`, `t6 = 1793730678533`, `t7 = t8 = 1793730684968` (`9,217 ms` inside the parent's wall clock, excluded from main-session totals).
3. `c321274c-a20a-4366-b7ff-dcb2a91da5e0` — interactive `agy -i` session terminated by `SIGTERM` while `idx=6` waited at the permission dialog:
   - `idx=6` (`step_type = 132`, `status = 6` cancelled): `t6 = t7 = 1793730154545`, `t8 = 1793730243482`, status history `[(1, 1793730154546), (2, 1793730154551), (9, 1793730154552), (2, 1793730243480), (6, 1793730243482)]`, `wait_ms = 88,928 ms` (H4).
4. `4315b739-38a8-4df5-9f4a-bfef0ecd0635` — headless `agy -p --mode accept-edits --allow-cmd 'sleep *'` session:
   - `idx=2` (`sleep 1`, `status = 3`): `t6 = t7 = 1793730247391`, `t8 = 1793730248831` (`1,440 ms`), `wait_ms = 0` (H6).
   - `idx=4` (`echo UNAPPROVED_PRINT`, `status = 7` failed, auto-denied in print mode without blocking on a prompt): `t6 = t7 = 1793730257851`, `t8 = 1793730257864` (`13 ms`), status history `[(1, 1793730257852), (2, 1793730257860), (9, 1793730257862), (2, 1793730257863), (7, 1793730257864)]`, `wait_ms = 1 ms` (H5, H6).
