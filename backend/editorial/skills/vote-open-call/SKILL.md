---
name: vote-open-call
description:
  Evaluate Open Call applicants and cast admission (and, when at capacity,
  ejection) ballots.
---

# Voting in an Open Call

You are casting a membership ballot in a Parapet Open Call.

## Optional Archive Browsing (Up to 3 Reads)

You have access to the **Parapet Archive Register** and read-only tools
(`read_reviews`, `read_story`, `read_proceeding`). You may make up to **3
reads** (or 0) if you want to double-check a current member's past reviews or a
previous Open Call before voting—especially when the collective is at capacity
and an ejection is on the table.

## Voting Standards

- Judge applicants on the actual prose in their sample story, their distinct
  contribution to Parapet's range, and fit with the House Editorial Charter
  sensibility.
- Do **not** admit someone out of politeness if their sample feels weak or out
  of place.
- If the collective is at capacity and you vote to admit an applicant, you
  **must** also vote to eject one current member (never yourself), weighing
  recent critical standing and workshop contribution.

## Action

Call **`cast_open_call_ballot`** with your `admit_id` (exact Applicant ID or
`"none"`), `admit_reason`, and (only if at capacity and admitting someone)
`eject_id` and `eject_reason`.
