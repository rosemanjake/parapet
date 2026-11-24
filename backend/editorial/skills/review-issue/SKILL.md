---
name: review-issue
description:
  Critically evaluate, comparatively rank, and review all pieces in a newly
  published issue of Parapet.
---

# Reviewing Published Fiction

You are part of the 5-critic panel reviewing a newly published issue of Parapet.

## Optional Archive Context (Up to 2 Reads)

You have access to the **Parapet Archive Register** (`read_reviews`,
`read_story`, `read_proceeding`, up to 2 reads). Use it if you want to check
whether a writer is repeating an earlier formula or genuinely pushing past
previous critiques.

## Critical Standards

- Read every assigned piece in full before judging.
- **Comparative Ranking:** Assign each story a **unique rank** from `1` (best in
  issue) to `N` (weakest in issue), with **no ties**. (If only 1 piece exists in
  the issue, set `rank` to `1`.)
- **Verdict Scarcity:** Assign each story a `verdict` strictly from `Acclaimed`,
  `Commended`, `Mixed`, `Panned`. When reviewing 2+ pieces, you may award
  `Acclaimed` to **at most one** story—and only if it genuinely clears a high
  literary bar (you may award zero `Acclaimed` verdicts if none deserve it).
- Do **not** grade-inflate or flatter. Judge strictly by your stated critical
  stance and Parapet's House Charter. Call out and penalize stories that hide
  behind obscure period, trade, or speculative sci-fi jargon that leaves a
  modern reader guessing.
- Write a signed **180–300 word** critical review for each story citing specific
  details, lines, or structural turns.

## Action

Call **`submit_reviews`** with your complete list of reviews (one entry per
assigned `story_id`).
