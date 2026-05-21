-- Iter v1 schema
-- Postgres 16+ with pgvector and pgcrypto extensions
-- Multi-tenant via Row-Level Security
-- Hot data: 90 days. Cold archive: Cloudflare R2 via manifest pointers in `archive_pointers`.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS citext;

-- ============================================================================
-- Tenancy
-- ============================================================================

CREATE TABLE tenants (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name            text NOT NULL,
  plan            text NOT NULL DEFAULT 'free' CHECK (plan IN ('free','team','enterprise')),
  created_at      timestamptz NOT NULL DEFAULT now(),
  deleted_at      timestamptz
);

CREATE TABLE users (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email           citext NOT NULL UNIQUE,
  display_name    text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  deleted_at      timestamptz
);

CREATE TABLE tenant_users (
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            text NOT NULL CHECK (role IN ('owner','admin','member')),
  joined_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX ON tenant_users(user_id);

-- ============================================================================
-- Sessions (one row per agent session, including subagents)
-- ============================================================================

CREATE TABLE sessions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_session_id   uuid REFERENCES sessions(id) ON DELETE CASCADE,
  harness             text NOT NULL,           -- 'claude_code','codex','gemini_cli','opencode','pi'
  model               text NOT NULL,           -- 'claude-opus-4-7','gpt-5-codex', etc
  effort              text CHECK (effort IN ('low','med','high','xhigh','max')),
  tools               text[] NOT NULL DEFAULT '{}', -- ['chrome','browser_use','computer_use', ...]
  repo_hash           text,                    -- hashed repo path; null for non-code work
  git_branch          text,
  started_at          timestamptz NOT NULL,
  ended_at            timestamptz,
  wall_time_ms        integer,
  turn_count          integer,
  total_tokens_in     bigint,
  total_tokens_out    bigint,
  redacted_prompt     text NOT NULL,           -- first user prompt, trufflehog-stripped
  redacted_system     text,                    -- system prompt, redacted
  classification      text NOT NULL CHECK (classification IN ('clean','strippable','dirty')),
  ingested_at         timestamptz NOT NULL DEFAULT now(),
  archived_at         timestamptz              -- set when moved to R2 cold
);

CREATE INDEX idx_sessions_tenant_user ON sessions(tenant_id, user_id, started_at DESC);
CREATE INDEX idx_sessions_tenant_started ON sessions(tenant_id, started_at DESC);
CREATE INDEX idx_sessions_parent ON sessions(parent_session_id) WHERE parent_session_id IS NOT NULL;
CREATE INDEX idx_sessions_repo ON sessions(tenant_id, repo_hash) WHERE repo_hash IS NOT NULL;

-- ============================================================================
-- Session events (append-only lifecycle and outcome log)
-- ============================================================================

CREATE TABLE session_events (
  id              bigserial PRIMARY KEY,
  session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  event_type      text NOT NULL CHECK (event_type IN (
                    'prompt_sent','tool_call','subagent_spawned','turn_completed',
                    'session_completed','user_override','git_commit','git_revert',
                    'pr_opened','pr_merged','pr_reverted','incident_linked',
                    'peer_reuse','self_reuse','suggestion_accepted','suggestion_rejected'
                  )),
  payload         jsonb NOT NULL,
  occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_events_session ON session_events(session_id, occurred_at);
CREATE INDEX idx_events_tenant_type_time ON session_events(tenant_id, event_type, occurred_at DESC);

-- ============================================================================
-- Session embeddings (pgvector ANN search for `iter suggest`)
-- ============================================================================

CREATE TABLE session_embeddings (
  session_id      uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  embedding       vector(1536) NOT NULL,
  embedding_model text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- HNSW for k-NN; rebuild plan documented when row count > 10M.
CREATE INDEX idx_embeddings_hnsw ON session_embeddings
  USING hnsw (embedding vector_cosine_ops)
  WITH (m = 16, ef_construction = 64);

CREATE INDEX idx_embeddings_tenant ON session_embeddings(tenant_id);

-- ============================================================================
-- Session scores (one or more scoring runs per session)
-- ============================================================================

CREATE TABLE session_scores (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  scorer_version  text NOT NULL,
  composite_score numeric(4,3) NOT NULL CHECK (composite_score BETWEEN 0 AND 1),
  signals         jsonb NOT NULL,             -- durability, peer_reuse, override_rate, etc
  rationale       text,
  contributor_weight numeric(4,3) NOT NULL DEFAULT 0.5 CHECK (contributor_weight BETWEEN 0 AND 1),
  scored_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_scores_session_time ON session_scores(session_id, scored_at DESC);
CREATE INDEX idx_scores_tenant_score ON session_scores(tenant_id, composite_score DESC, scored_at DESC);

-- ============================================================================
-- Outcomes (links sessions to downstream git / incident events)
-- ============================================================================

CREATE TABLE outcomes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  outcome_type    text NOT NULL CHECK (outcome_type IN (
                    'commit_landed','pr_merged','pr_reverted','code_reverted_within_7d',
                    'tests_passed','tests_failed','incident_caused','peer_referenced'
                  )),
  external_ref    text,                       -- commit SHA, PR URL, incident ID
  details         jsonb,
  observed_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_outcomes_session ON outcomes(session_id, observed_at DESC);
CREATE INDEX idx_outcomes_tenant_type ON outcomes(tenant_id, outcome_type, observed_at DESC);

-- ============================================================================
-- Suggested prompts (cached refinements; looked up by embedding similarity)
-- ============================================================================

CREATE TABLE suggestions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  source_prompt   text NOT NULL,              -- redacted
  source_embedding vector(1536) NOT NULL,
  refined_prompt  text NOT NULL,
  rationale       text,
  evidence_session_ids uuid[] NOT NULL,       -- traces used to derive this suggestion
  hit_count       integer NOT NULL DEFAULT 0,
  accept_count    integer NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_used_at    timestamptz
);

CREATE INDEX idx_suggestions_tenant ON suggestions(tenant_id, last_used_at DESC NULLS LAST);
CREATE INDEX idx_suggestions_embedding ON suggestions
  USING hnsw (source_embedding vector_cosine_ops)
  WITH (m = 16, ef_construction = 64);

-- ============================================================================
-- Stacks (shareable lightweight stacks — NOT raw configs)
-- ============================================================================

CREATE TABLE stacks (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            text NOT NULL,              -- "Ben's stack"
  harnesses       text[] NOT NULL,            -- ['warp','claude_code','codex_cli']
  skills          text[] NOT NULL DEFAULT '{}',
  docs            text[] NOT NULL DEFAULT '{}', -- references the user chose to include
  notes           text,                       -- free-form, trufflehog-scanned
  classification  text NOT NULL CHECK (classification IN ('clean','strippable','dirty')),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_stacks_user ON stacks(tenant_id, user_id, updated_at DESC);

CREATE TABLE stack_shares (
  stack_id            uuid NOT NULL REFERENCES stacks(id) ON DELETE CASCADE,
  tenant_id           uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  shared_with_user_id uuid REFERENCES users(id) ON DELETE CASCADE, -- null = whole tenant team
  shared_at           timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (stack_id, shared_with_user_id)
);

CREATE INDEX idx_stack_shares_target ON stack_shares(tenant_id, shared_with_user_id);

-- ============================================================================
-- Archive pointers (cold R2 object references for sessions older than 90 days)
-- ============================================================================

CREATE TABLE archive_pointers (
  session_id      uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL,
  object_uri      text NOT NULL,           -- r2://iter-archive-prod/<tenant_id>/<session_id>.tar.zst
  archived_at     timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================
-- Audit log (append-only; security and compliance events)
-- ============================================================================

CREATE TABLE audit_log (
  id              bigserial PRIMARY KEY,
  tenant_id       uuid REFERENCES tenants(id) ON DELETE SET NULL, -- nullable for cross-tenant ops
  actor_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,   -- nullable for system actors
  actor_kind      text NOT NULL CHECK (actor_kind IN ('user','admin','system','batch_job')),
  event_type      text NOT NULL CHECK (event_type IN (
                    'tenant_created','tenant_deleted',
                    'user_created','user_deleted','user_left_team',
                    'stack_shared','stack_unshared',
                    'leak_detected_post_ingestion','session_cascade_deleted',
                    'score_model_rollback',
                    'permissions_revoked','permissions_granted',
                    'admin_action','data_export_requested','data_deletion_requested'
                  )),
  target_kind     text,                       -- 'session','user','tenant','stack', etc
  target_id       text,                       -- not FK; targets may be deleted
  details         jsonb NOT NULL DEFAULT '{}',
  occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_tenant_time ON audit_log(tenant_id, occurred_at DESC);
CREATE INDEX idx_audit_event_time ON audit_log(event_type, occurred_at DESC);
CREATE INDEX idx_audit_actor ON audit_log(actor_user_id, occurred_at DESC) WHERE actor_user_id IS NOT NULL;

-- ============================================================================
-- Row-Level Security
-- ============================================================================
-- App must SET LOCAL app.current_tenant = '<uuid>' at the start of each tx.

ALTER TABLE sessions             ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_events       ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_embeddings   ENABLE ROW LEVEL SECURITY;
ALTER TABLE session_scores       ENABLE ROW LEVEL SECURITY;
ALTER TABLE outcomes             ENABLE ROW LEVEL SECURITY;
ALTER TABLE suggestions          ENABLE ROW LEVEL SECURITY;
ALTER TABLE stacks               ENABLE ROW LEVEL SECURITY;
ALTER TABLE stack_shares         ENABLE ROW LEVEL SECURITY;
ALTER TABLE archive_pointers     ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log            ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON sessions
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON session_events
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON session_embeddings
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON session_scores
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON outcomes
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON suggestions
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON stacks
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON stack_shares
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON archive_pointers
  USING (tenant_id = current_setting('app.current_tenant')::uuid);
CREATE POLICY tenant_isolation ON audit_log
  USING (tenant_id = current_setting('app.current_tenant')::uuid);

-- Bypass role for the nightly scoring batch and the archive job.
-- Use only with explicit SET ROLE iter_batch; never from the request path.
CREATE ROLE iter_batch BYPASSRLS;
