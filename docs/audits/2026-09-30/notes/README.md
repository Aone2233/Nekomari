# Working notes from the 2026-09-30 accuracy repair

Three files that were sitting untracked in the repository root after the
2026-09-30 repair session, moved here verbatim on 2026-10-01 so the working tree
stops carrying loose files and the notes stop being one `rm` away from gone.

| File | What it is |
|---|---|
| `findings.md` | The repair findings as they were written up at the time: baseline commit, what was confirmed live, what was and was not guaranteed. |
| `progress.md` | What was completed locally, phase by phase, with the checks that were run. |
| `task_plan.md` | The plan and gates the session worked to, including the boundaries it promised not to cross. |

They are **working notes, not the record.** The record for that session is
[`../MONITORING-ACCURACY-FIXES-2026-09-30.md`](../MONITORING-ACCURACY-FIXES-2026-09-30.md)
and the acceptance write-up is [`../../DEPLOY-OC424.md`](../../DEPLOY-OC424.md).
Where the two disagree, the record wins — these files were not updated after the
work moved on, and at least one of them still describes the fleet as it was
before the v1.6.5 rollout.
