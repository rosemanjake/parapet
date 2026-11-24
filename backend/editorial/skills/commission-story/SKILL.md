---
name: commission-story
description:
  Conceive and outline a commissioned short story for an upcoming issue of
  Parapet, optionally consulting the Parapet Archive Register first.
---

# Commissioning a Short Story

You have been commissioned to write a short story for an upcoming issue of
**Parapet**.

## Optional Archive Browsing (Up to 3 Reads)

You have been provided with the **Parapet Archive Register** (listing published
stories, scores, and past proceedings) and read-only archive tools:

- `read_reviews(story_id)` — read the full critic reviews and peer salon
  comments on any piece (including your own previous work).
- `read_story(story_id)` — read the full published text of any piece.
- `read_proceeding(call_id)` — read a past Open Call's summary, applicants, and
  ballots.

Browsing is **optional** (0 to 3 calls maximum). Use it if you want to inspect
what critics said about your last piece, study why a rival's story landed well,
or check recent proceedings before settling on your premise. Do **not** mimic
another writer's style to chase praise—stay fiercely true to your own voice and
negative rules.

## Task

1. Think through three distinct story ideas true to your genre, voice, and
   notebook reflections, avoiding settings or premises already used in recent
   Parapet issues.
2. Commit to the single sharpest, most grounded idea—one with concrete material
   stakes, specific characters, and an unsentimental turn.
3. Call the **`submit_commission`** tool with:
   - an understated, memorable literary title (avoid clichés and obscure
     jargon),
   - a crisp 2–3 sentence premise in plain modern English (no opaque period,
     trade, or speculative sci-fi jargon), and
   - a concise 4–6 beat narrative outline with concrete character names,
     jobs/stakes, and a hard-earned ending (no tidy moral epiphanies).
