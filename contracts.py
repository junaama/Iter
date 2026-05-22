"""
Iter v1 — wire contracts for daemon, CLI, dashboard, and webhooks.

Pure type definitions. No I/O. No business logic. The boundary between this
file and the rest of the codebase is the only place where untyped JSON is
allowed to exist.

Conventions:
- All IDs are UUIDs (str at the wire, validated by pydantic).
- All timestamps are ISO 8601, UTC, with timezone marker.
- Discriminated unions use a `type` field; pydantic handles via `Field(discriminator=...)`.
- Pure functions only in this module. Impure handlers live elsewhere and
  consume/produce these types.
"""

from __future__ import annotations

from datetime import date, datetime
from enum import Enum
from typing import Annotated, Literal, Optional, Union
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, field_validator


# ============================================================================
# Shared enums
# ============================================================================

class Harness(str, Enum):
    CLAUDE_CODE = "claude_code"
    CODEX = "codex"
    GEMINI_CLI = "gemini_cli"
    OPENCODE = "opencode"
    PI = "pi"


class Effort(str, Enum):
    LOW = "low"
    MED = "med"
    HIGH = "high"
    XHIGH = "xhigh"
    MAX = "max"


class Classification(str, Enum):
    CLEAN = "clean"
    STRIPPABLE = "strippable"
    DIRTY = "dirty"


class EventType(str, Enum):
    PROMPT_SENT = "prompt_sent"
    TOOL_CALL = "tool_call"
    SUBAGENT_SPAWNED = "subagent_spawned"
    TURN_COMPLETED = "turn_completed"
    SESSION_COMPLETED = "session_completed"
    USER_OVERRIDE = "user_override"
    GIT_COMMIT = "git_commit"
    GIT_REVERT = "git_revert"
    PR_OPENED = "pr_opened"
    PR_MERGED = "pr_merged"
    PR_REVERTED = "pr_reverted"
    INCIDENT_LINKED = "incident_linked"
    PEER_REUSE = "peer_reuse"
    SELF_REUSE = "self_reuse"
    SUGGESTION_ACCEPTED = "suggestion_accepted"
    SUGGESTION_REJECTED = "suggestion_rejected"


class OutcomeType(str, Enum):
    COMMIT_LANDED = "commit_landed"
    PR_MERGED = "pr_merged"
    PR_REVERTED = "pr_reverted"
    CODE_REVERTED_WITHIN_7D = "code_reverted_within_7d"
    TESTS_PASSED = "tests_passed"
    TESTS_FAILED = "tests_failed"
    INCIDENT_CAUSED = "incident_caused"
    PEER_REFERENCED = "peer_referenced"


# Confidence thresholds (locked phase 5):
#   < 0.50  → suppress (do not surface)
#   < 0.80  → advisory (surface, do not replace)
#   >= 0.80 → replace prompt
CONFIDENCE_SUPPRESS_BELOW = 0.50
CONFIDENCE_REPLACE_AT = 0.80


# ============================================================================
# Session context — shared by daemon + CLI suggest call
# ============================================================================

class SessionContext(BaseModel):
    model_config = ConfigDict(extra="forbid")

    harness: Harness
    model: str
    effort: Optional[Effort] = None
    tools: list[str] = Field(default_factory=list)
    repo_hash: Optional[str] = None
    git_branch: Optional[str] = None
    cwd_files: list[str] = Field(default_factory=list)
    raw_prompt: str = Field(min_length=1, max_length=200_000)


# ============================================================================
# CLI ↔ cloud: POST /v1/suggest
# ============================================================================

class SuggestOptions(BaseModel):
    model_config = ConfigDict(extra="forbid")

    max_latency_ms: int = Field(default=800, ge=50, le=5000)
    include_evidence: bool = False


class SuggestRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    user_id: UUID
    tenant_id: UUID
    session_context: SessionContext
    options: SuggestOptions = Field(default_factory=SuggestOptions)


class SuggestEvidence(BaseModel):
    model_config = ConfigDict(extra="forbid")

    session_id: UUID
    outcome: OutcomeType
    wall_time_ms: Optional[int] = None
    contributor_display_name: str


class NoSuggestionReason(str, Enum):
    NO_EVIDENCE = "no_evidence"          # no nearby past sessions to learn from
    LOW_CONFIDENCE = "low_confidence"    # candidates exist but all score < 0.50
    LLM_UNAVAILABLE = "llm_unavailable"  # upstream LLM provider down
    LATENCY_BUDGET_EXCEEDED = "latency_budget_exceeded"
    USER_OPTED_OUT = "user_opted_out"


class SuggestResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    suggestion_id: Optional[UUID]
    refined_prompt: Optional[str]
    rationale: Optional[str]
    confidence: float = Field(ge=0.0, le=1.0)
    evidence: list[SuggestEvidence] = Field(default_factory=list)
    no_suggestion_reason: Optional[NoSuggestionReason] = None

    @field_validator("refined_prompt")
    @classmethod
    def _refined_present_when_replacing(cls, v: Optional[str], info):
        # If confidence >= replace threshold, refined_prompt must be present.
        # We don't have other field values reliably here for cross-field check,
        # so this validator just ensures non-empty when given. See SuggestResponse.
        if v is not None and not v.strip():
            raise ValueError("refined_prompt must be non-empty if present")
        return v


def suggestion_action(confidence: float, refined_prompt: Optional[str]) -> Literal["suppress", "advise", "replace"]:
    """Pure decision function. Reflects locked thresholds.
    Clients (skill.md handler, post-prompt monitor) call this to decide UI behavior."""
    if refined_prompt is None or confidence < CONFIDENCE_SUPPRESS_BELOW:
        return "suppress"
    if confidence >= CONFIDENCE_REPLACE_AT:
        return "replace"
    return "advise"


# ============================================================================
# CLI ↔ cloud: stacks
# ============================================================================

class StackPayload(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str = Field(min_length=1, max_length=120)
    harnesses: list[str] = Field(min_length=1)
    skills: list[str] = Field(default_factory=list)
    docs: list[str] = Field(default_factory=list)
    notes: Optional[str] = Field(default=None, max_length=10_000)


class StackUpsertRequest(StackPayload):
    pass


class StackResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: UUID
    user_id: UUID
    payload: StackPayload
    classification: Classification
    created_at: datetime
    updated_at: datetime


class StackShareRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    # null target = share with whole tenant team
    shared_with_user_id: Optional[UUID] = None


# ============================================================================
# Dashboard ↔ cloud
# ============================================================================

class ScoreSignals(BaseModel):
    model_config = ConfigDict(extra="allow")  # signals evolve; allow extras

    durability_7d: Optional[float] = None
    durability_30d: Optional[float] = None
    peer_reuse_count: Optional[int] = None
    self_reuse_count: Optional[int] = None
    override_rate: Optional[float] = None
    suggestion_acceptance: Optional[float] = None


class SessionScoreView(BaseModel):
    model_config = ConfigDict(extra="forbid")

    session_id: UUID
    composite_score: float = Field(ge=0.0, le=1.0)
    signals: ScoreSignals
    contributor_weight: float = Field(ge=0.0, le=1.0)
    scored_at: datetime
    rationale: Optional[str] = None


class SessionSummary(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: UUID
    user_id: UUID
    parent_session_id: Optional[UUID]
    harness: Harness
    model: str
    effort: Optional[Effort]
    tools: list[str]
    started_at: datetime
    ended_at: Optional[datetime]
    wall_time_ms: Optional[int]
    turn_count: Optional[int]
    redacted_prompt: str
    latest_score: Optional[SessionScoreView] = None


class DashboardUser(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: UUID
    display_name: str
    email: str


class DashboardTrendPoint(BaseModel):
    model_config = ConfigDict(extra="forbid")

    date: date
    composite_score: Optional[float] = Field(default=None, ge=0.0, le=1.0)
    session_count: int = Field(ge=0)


class DashboardRecentSession(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: UUID
    started_at: datetime
    composite_score: Optional[float] = Field(default=None, ge=0.0, le=1.0)
    harness: Harness
    redacted_prompt_preview: str = Field(max_length=123)


class DashboardMeResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    user: DashboardUser
    trend: list[DashboardTrendPoint]
    recent_sessions: list[DashboardRecentSession]


class DashboardTeamResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    tenant_id: UUID
    members: list["TeamMemberAggregate"]


class TeamMemberAggregate(BaseModel):
    model_config = ConfigDict(extra="forbid")

    user_id: UUID
    display_name: str
    sessions_7d: int
    avg_score_7d: Optional[float]
    contributor_weight: float


DashboardTeamResponse.model_rebuild()


# ============================================================================
# Daemon ↔ cloud: WebSocket message envelope
# ============================================================================

# All daemon messages share an envelope with a discriminator on `type`.

class _WSBase(BaseModel):
    model_config = ConfigDict(extra="forbid")
    msg_id: UUID
    sent_at: datetime


# --- client → server ---

class AuthHello(_WSBase):
    type: Literal["auth.hello"] = "auth.hello"
    bearer_token: str = Field(min_length=20)
    daemon_version: str
    os_version: str


class TraceSessionStarted(_WSBase):
    type: Literal["trace.session_started"] = "trace.session_started"
    session_id: UUID
    parent_session_id: Optional[UUID]
    user_id: UUID
    tenant_id: UUID
    session_context: SessionContext
    started_at: datetime


class TraceEvent(_WSBase):
    type: Literal["trace.event"] = "trace.event"
    session_id: UUID
    event_type: EventType
    payload: dict  # tool-call shape varies by harness; opaque to wire
    occurred_at: datetime


class TraceSessionCompleted(_WSBase):
    type: Literal["trace.session_completed"] = "trace.session_completed"
    session_id: UUID
    ended_at: datetime
    wall_time_ms: int
    turn_count: int
    total_tokens_in: int
    total_tokens_out: int
    classification: Classification


class StackUpsert(_WSBase):
    type: Literal["stack.upsert"] = "stack.upsert"
    user_id: UUID
    payload: StackPayload


class StackShare(_WSBase):
    type: Literal["stack.share"] = "stack.share"
    stack_id: UUID
    shared_with_user_id: Optional[UUID]  # null = whole team


class Ack(_WSBase):
    type: Literal["ack"] = "ack"
    ack_msg_id: UUID


ClientMessage = Annotated[
    Union[
        AuthHello,
        TraceSessionStarted,
        TraceEvent,
        TraceSessionCompleted,
        StackUpsert,
        StackShare,
        Ack,
    ],
    Field(discriminator="type"),
]


# --- server → client ---

class SuggestionAvailable(_WSBase):
    type: Literal["suggestion.available"] = "suggestion.available"
    session_id: UUID
    suggestion: SuggestResponse


class SuggestionPreempt(_WSBase):
    """Server proactively pushes a suggestion the user didn't request, e.g.
    detected a recurring pattern across team and wants to surface it on next prompt."""
    type: Literal["suggestion.preempt"] = "suggestion.preempt"
    user_id: UUID
    advisory_text: str


class ServerError(_WSBase):
    type: Literal["error"] = "error"
    code: str
    detail: str


ServerMessage = Annotated[
    Union[
        SuggestionAvailable,
        SuggestionPreempt,
        Ack,
        ServerError,
    ],
    Field(discriminator="type"),
]


# ============================================================================
# Webhooks
# ============================================================================

class GithubWebhookEvent(BaseModel):
    """Subset of GitHub events Iter cares about: PR merged, PR closed without merge,
    commit pushed, revert detected. HMAC verified before this type is constructed."""
    model_config = ConfigDict(extra="ignore")

    event: Literal["pull_request", "push"]
    repo_full_name: str
    sha: Optional[str] = None
    pr_number: Optional[int] = None
    action: Optional[str] = None  # 'merged', 'closed', etc
    is_revert: bool = False
    occurred_at: datetime


class LinearWebhookEvent(BaseModel):
    """Subset of Linear events: issue closed, status changed, comment posted.
    Signing-secret verified before construction."""
    model_config = ConfigDict(extra="ignore")

    event_type: str
    issue_id: str
    state: Optional[str] = None
    occurred_at: datetime


# ============================================================================
# Decision shapes used by pure scoring functions
# ============================================================================

class CompositeScoreInputs(BaseModel):
    """Inputs to the scoring function. Pure: same inputs → same score."""
    model_config = ConfigDict(extra="forbid")

    durability_7d: Optional[float] = None
    durability_30d: Optional[float] = None
    peer_reuse_count: int = 0
    self_reuse_count: int = 0
    override_rate: Optional[float] = None
    suggestion_acceptance: Optional[float] = None
    wall_time_ms: Optional[int] = None
    turn_count: Optional[int] = None
    contributor_weight: float = 0.5


class CompositeScoreOutput(BaseModel):
    model_config = ConfigDict(extra="forbid")

    composite_score: float = Field(ge=0.0, le=1.0)
    signals_used: ScoreSignals
    rationale: str
