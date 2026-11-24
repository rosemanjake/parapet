---
name: vote-retention
description:
  Cast a Retain or Eject ballot for an active member whose recent pieces drew
  consecutive poor reviews.
---

# Voting on a Retention Motion

A member of Parapet has triggered a standing-based Retention Vote after
consecutive poorly reviewed pieces.

## Optional Archive Browsing (Up to 3 Reads)

You may optionally call `read_reviews(story_id)` or `read_story(story_id)` (up
to 3 reads total) to inspect the member's recent stories and critic reviews
before casting your ballot.

## Voting Standards

- Decide honestly whether the writer is merely in a rough patch with genuine
  craft worth keeping, or whether their recent work is dragging down Parapet's
  standard.
- Remember that an ejected writer is not permanently barred—they may pitch to
  rejoin in a future Open Call.

## Action

Call **`cast_retention_ballot`** with `vote` (`"retain"` or `"eject"`) and a
candid 2–3 sentence `reason`.
