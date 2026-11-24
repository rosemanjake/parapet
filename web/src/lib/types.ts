export interface TokenUsage {
  input_tokens: number;
  output_tokens: number;
  thought_tokens?: number;
  calls: number;
}

export interface State {
  current_issue: number;
  next_phase: string;
  author_queue: string[];
  max_members: number;
  stories_per_issue: number;
  issues_per_open_call: number;
  charter: string;
  token_usage: TokenUsage;
  last_tick_at?: string;
  lock_owner?: string;
  lock_until?: string;
}

export interface Relationship {
  trust: number;
  respect: number;
  impression: string;
}

export interface StandingEntry {
  issue_number: number;
  story_id: string;
  story_title: string;
  verdicts: string[];
  average_rank: number;
}

export interface Agent {
  id: string;
  name: string;
  role: "writer" | "critic";
  status: "active" | "ejected";
  genre: string;
  bio: string;
  voice_craft: string;
  negative_rules: string;
  editorial_temperament: string;
  notebook?: string;
  standing?: StandingEntry[];
  relationships?: Record<string, Relationship>;
  is_founder?: boolean;
  joined_issue: number;
  ejected_issue?: number;
  created_at?: string;
}

export interface StorySummary {
  id: string;
  slug: string;
  title: string;
  author_id: string;
  author_name: string;
  genre: string;
  synopsis?: string;
  epigraph?: string;
  image_url?: string;
}

export interface Issue {
  number: number;
  status: string;
  published_at?: string;
  stories: StorySummary[];
}

export interface Review {
  critic_id: string;
  critic_name: string;
  critic_role: string;
  rank: number;
  verdict: "Acclaimed" | "Commended" | "Mixed" | "Panned" | string;
  body: string;
  created_at?: string;
}

export interface SalonComment {
  author_id: string;
  author_name: string;
  author_genre: string;
  body: string;
  created_at?: string;
}

export interface Story {
  id: string;
  slug: string;
  issue_number: number;
  status: string;
  author_id: string;
  author_name: string;
  genre: string;
  title: string;
  premise?: string;
  outline?: string;
  consulted?: string[];
  synopsis?: string;
  epigraph?: string;
  image_prompt?: string;
  image_url?: string;
  draft_v1?: string;
  editor_id?: string;
  editor_name?: string;
  editor_select_reason?: string;
  editor_notes?: string;
  author_revision_note?: string;
  final_text: string;
  reviews?: Review[];
  salon_comments?: SalonComment[];
  created_at?: string;
  published_at?: string;
}

export interface Applicant {
  id: string;
  name: string;
  genre: string;
  bio: string;
  cover_letter: string;
  sample_title: string;
  sample_story: string;
  is_returning_alumni?: boolean;
}

export interface Ballot {
  voter_id: string;
  voter_name: string;
  admit_id: string;
  admit_reason: string;
  eject_id?: string;
  eject_reason?: string;
}

export interface RetentionBallot {
  voter_id: string;
  voter_name: string;
  vote: "retain" | "eject" | string;
  reason: string;
}

export interface RetentionMotion {
  target_id: string;
  target_name: string;
  trigger_reason: string;
  ballots: RetentionBallot[];
  retain_votes: number;
  eject_votes: number;
  ejected: boolean;
  summary: string;
}

export interface OpenCall {
  id: string;
  after_issue: number;
  applicants: Applicant[];
  ballots: Ballot[];
  admitted_ids?: string[];
  ejected_ids?: string[];
  retention_motions?: RetentionMotion[];
  summary: string;
  resolved_at?: string;
}
