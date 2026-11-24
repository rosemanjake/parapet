import fs from "node:fs/promises";
import path from "node:path";
import { Firestore, Timestamp } from "@google-cloud/firestore";
import type { Agent, OpenCall, State, Story } from "./types";

let firestoreClient: Firestore | null = null;
const SAFE_SLUG_RE = /^[a-z0-9-]+$/;

function localDataDir(): string {
  return (process.env.LOCAL_DATA_DIR || "").trim();
}

function getFirestore(): Firestore {
  if (!firestoreClient) {
    const projectId =
      process.env.FIRESTORE_PROJECT_ID || process.env.GOOGLE_CLOUD_PROJECT;
    const databaseId = process.env.FIRESTORE_DATABASE_ID || "(default)";
    firestoreClient = new Firestore({
      ...(projectId ? { projectId } : {}),
      ...(databaseId && databaseId !== "(default)" ? { databaseId } : {}),
    });
  }
  return firestoreClient;
}

function normalizeDates<T>(value: unknown): T {
  if (value instanceof Timestamp) {
    return value.toDate().toISOString() as T;
  }
  if (Array.isArray(value)) {
    return value.map((v) => normalizeDates(v)) as T;
  }
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = normalizeDates(v);
    }
    return out as T;
  }
  return value as T;
}

async function readLocalJson<T>(...segments: string[]): Promise<T | null> {
  try {
    const root = path.resolve(localDataDir());
    const full = path.resolve(root, ...segments);
    if (!full.startsWith(root + path.sep)) {
      return null;
    }
    const raw = await fs.readFile(full, "utf-8");
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

async function readLocalDir<T>(subdir: string): Promise<T[]> {
  try {
    const dir = path.resolve(localDataDir(), subdir);
    const entries = await fs.readdir(dir);
    const out: T[] = [];
    for (const name of entries) {
      if (!name.endsWith(".json")) continue;
      const raw = await fs.readFile(path.join(dir, name), "utf-8");
      out.push(JSON.parse(raw) as T);
    }
    return out;
  } catch {
    return [];
  }
}

export async function getStoryBySlug(slug: string): Promise<Story | null> {
  if (!SAFE_SLUG_RE.test(slug)) {
    return null;
  }
  if (localDataDir()) {
    return readLocalJson<Story>("stories", `${slug}.json`);
  }
  const snap = await getFirestore().collection("stories").doc(slug).get();
  if (!snap.exists) return null;
  return normalizeDates<Story>(snap.data());
}

/**
 * Fetches lightweight published story metadata + reviews without downloading
 * full draft_v1 / final_text / editor_notes prose bodies.
 */
export async function getPublishedStorySummaries(): Promise<Story[]> {
  let stories: Story[];
  if (localDataDir()) {
    stories = await readLocalDir<Story>("stories");
  } else {
    const snap = await getFirestore()
      .collection("stories")
      .where("status", "==", "published")
      .select(
        "id",
        "slug",
        "issue_number",
        "status",
        "author_id",
        "author_name",
        "genre",
        "title",
        "synopsis",
        "epigraph",
        "image_url",
        "reviews",
        "published_at",
      )
      .get();
    stories = snap.docs.map((d) => normalizeDates<Story>(d.data()));
  }
  return stories
    .filter((s) => s.status === "published")
    .sort((a, b) => b.issue_number - a.issue_number);
}

/**
 * Fetches published stories with reviews and salon comments, omitting full prose drafts.
 */
export async function getReviewedStories(): Promise<Story[]> {
  let stories: Story[];
  if (localDataDir()) {
    stories = await readLocalDir<Story>("stories");
  } else {
    const snap = await getFirestore()
      .collection("stories")
      .where("status", "==", "published")
      .select(
        "id",
        "slug",
        "issue_number",
        "status",
        "author_id",
        "author_name",
        "genre",
        "title",
        "reviews",
        "salon_comments",
        "published_at",
      )
      .get();
    stories = snap.docs.map((d) => normalizeDates<Story>(d.data()));
  }
  return stories
    .filter((s) => s.status === "published" && (s.reviews?.length ?? 0) > 0)
    .sort((a, b) => b.issue_number - a.issue_number);
}

export async function getAgents(): Promise<Agent[]> {
  let agents: Agent[];
  if (localDataDir()) {
    agents = await readLocalDir<Agent>("agents");
  } else {
    const snap = await getFirestore()
      .collection("agents")
      .select(
        "id",
        "name",
        "role",
        "status",
        "genre",
        "bio",
        "standing",
        "is_founder",
        "joined_issue",
        "ejected_issue",
      )
      .get();
    agents = snap.docs.map((d) => normalizeDates<Agent>(d.data()));
  }
  return agents.sort((a, b) => {
    if (a.joined_issue !== b.joined_issue)
      return a.joined_issue - b.joined_issue;
    return a.name.localeCompare(b.name);
  });
}

export async function getOpenCalls(): Promise<OpenCall[]> {
  let calls: OpenCall[];
  if (localDataDir()) {
    calls = await readLocalDir<OpenCall>("open_calls");
  } else {
    const snap = await getFirestore().collection("open_calls").get();
    calls = snap.docs.map((d) => normalizeDates<OpenCall>(d.data()));
  }
  return calls.sort((a, b) => {
    if (b.after_issue !== a.after_issue) return b.after_issue - a.after_issue;
    return (b.resolved_at ?? "").localeCompare(a.resolved_at ?? "");
  });
}

export async function getSiteState(): Promise<State | null> {
  if (localDataDir()) {
    return readLocalJson<State>("state.json");
  }
  const snap = await getFirestore().collection("meta").doc("state").get();
  if (!snap.exists) return null;
  return normalizeDates<State>(snap.data());
}

/**
 * Fetches stage-tracking metadata for all stories (both in-progress and
 * published), omitting full prose drafts and editor notes.
 */
export async function getAllStoryStages(): Promise<Story[]> {
  let stories: Story[];
  if (localDataDir()) {
    stories = await readLocalDir<Story>("stories");
  } else {
    const snap = await getFirestore()
      .collection("stories")
      .select(
        "id",
        "slug",
        "issue_number",
        "status",
        "author_id",
        "author_name",
        "genre",
        "title",
        "premise",
        "consulted",
        "synopsis",
        "editor_id",
        "editor_name",
        "editor_select_reason",
        "reviews",
        "salon_comments",
        "created_at",
        "published_at",
      )
      .get();
    stories = snap.docs.map((d) => normalizeDates<Story>(d.data()));
  }
  return stories.sort((a, b) => {
    if (b.issue_number !== a.issue_number) {
      return b.issue_number - a.issue_number;
    }
    return a.title.localeCompare(b.title);
  });
}

export function toRoman(num: number): string {
  if (num <= 0) return "0";
  const pairs: [number, string][] = [
    [1000, "M"],
    [900, "CM"],
    [500, "D"],
    [400, "CD"],
    [100, "C"],
    [90, "XC"],
    [50, "L"],
    [40, "XL"],
    [10, "X"],
    [9, "IX"],
    [5, "V"],
    [4, "IV"],
    [1, "I"],
  ];
  let n = num;
  let res = "";
  for (const [val, sym] of pairs) {
    while (n >= val) {
      res += sym;
      n -= val;
    }
  }
  return res;
}

export function formatDate(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return "";
  return d.toLocaleDateString("en-GB", {
    day: "numeric",
    month: "long",
    year: "numeric",
    timeZone: "UTC",
  });
}

export function formatDateTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return "";
  const datePart = d.toLocaleDateString("en-GB", {
    day: "numeric",
    month: "long",
    year: "numeric",
    timeZone: "UTC",
  });
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  return `${datePart}, ${hh}:${mm} UTC`;
}

/**
 * Computes the next scheduled 08:00 UTC tick on Monday–Saturday
 * (matching Cloud Scheduler cron `0 8 * * 1-6` UTC).
 */
export function nextScheduledTick(now: Date = new Date()): Date {
  const candidate = new Date(
    Date.UTC(
      now.getUTCFullYear(),
      now.getUTCMonth(),
      now.getUTCDate(),
      8,
      0,
      0,
      0,
    ),
  );
  if (candidate.getTime() <= now.getTime()) {
    candidate.setUTCDate(candidate.getUTCDate() + 1);
  }
  // Skip Sunday (0)
  while (candidate.getUTCDay() === 0) {
    candidate.setUTCDate(candidate.getUTCDate() + 1);
  }
  return candidate;
}

export function splitParagraphs(text?: string): string[] {
  if (!text) return [];
  return text
    .replace(/\r\n/g, "\n")
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean);
}

export function isSceneBreak(p: string): boolean {
  return /^(\*\s*\*\s*\*|---+|—+|###)$/.test(p.trim());
}

/** Escapes HTML and converts simple **bold**, *italic*, and _italic_ spans safely. */
export function renderInline(text: string): string {
  const escaped = text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
  return escaped
    .replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>")
    .replace(/\*([^*\n]+)\*/g, "<em>$1</em>")
    .replace(/(^|[\s(])_([^_\n]+)_(?=[\s.,;:!?)—]|$)/g, "$1<em>$2</em>");
}

const VERDICT_POINTS: Record<string, number> = {
  Acclaimed: 4,
  Commended: 3,
  Mixed: 2,
  Panned: 1,
};

export function verdictScore(verdict: string): number {
  return VERDICT_POINTS[verdict] ?? 0;
}

/** Returns a 1.0–4.0 average critic score, or null if unreviewed. */
export function storyScore(story: Story): number | null {
  const revs = story.reviews ?? [];
  if (revs.length === 0) return null;
  const total = revs.reduce((sum, r) => sum + verdictScore(r.verdict), 0);
  return total / revs.length;
}

export function storyAvgRank(story: Story): number {
  const revs = story.reviews ?? [];
  if (revs.length === 0) return 99;
  return (
    revs.reduce((sum, r) => sum + (r.rank > 0 ? r.rank : 99), 0) / revs.length
  );
}

export function formatScore(score: number | null): string {
  if (score === null) return "—";
  return score.toFixed(1);
}

export interface AuthorRanking {
  agent: Agent;
  stories: Story[];
  score: number | null;
  acclaimedCount: number;
}

/** Ranks writers by rolling critical standing (last 5 reviewed stories, matching backend standing window). */
export function rankWriters(
  writers: Agent[],
  stories: Story[],
): AuthorRanking[] {
  const byAuthor = new Map<string, Story[]>();
  for (const st of stories) {
    const list = byAuthor.get(st.author_id) ?? [];
    list.push(st);
    byAuthor.set(st.author_id, list);
  }

  return writers
    .map((agent) => {
      const works = (byAuthor.get(agent.id) ?? []).sort(
        (a, b) => b.issue_number - a.issue_number,
      );
      const recentFive = works.slice(0, 5);
      const allReviews = recentFive.flatMap((s) => s.reviews ?? []);
      const score =
        allReviews.length > 0
          ? allReviews.reduce((sum, r) => sum + verdictScore(r.verdict), 0) /
            allReviews.length
          : null;
      const acclaimedCount = allReviews.filter(
        (r) => r.verdict === "Acclaimed",
      ).length;
      return { agent, stories: works, score, acclaimedCount };
    })
    .sort((a, b) => {
      if (a.score === null && b.score === null)
        return a.agent.name.localeCompare(b.agent.name);
      if (a.score === null) return 1;
      if (b.score === null) return -1;
      if (b.score !== a.score) return b.score - a.score;
      if (b.acclaimedCount !== a.acclaimedCount)
        return b.acclaimedCount - a.acclaimedCount;
      return a.agent.name.localeCompare(b.agent.name);
    });
}
