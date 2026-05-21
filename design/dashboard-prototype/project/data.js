// Sample data for the iter dashboard prototype.
// Realistic engineer names + real-sounding repos/tasks.

window.ITER_DATA = (() => {
  const me = {
    name: 'Priya Raman',
    handle: 'priya',
    avatar: 'PR',
    role: 'Staff Engineer',
    tenant: 'iter · core',
  };

  const teammates = [
    { name: 'Priya Raman',     handle: 'priya',   avatar: 'PR', status: 'active'  },
    { name: 'Marcus Chen',     handle: 'mchen',   avatar: 'MC', status: 'active'  },
    { name: 'Ana Volkov',      handle: 'ana',     avatar: 'AV', status: 'active'  },
    { name: 'Yusuf Doumbia',   handle: 'yusuf',   avatar: 'YD', status: 'paused'  },
    { name: 'Lena Falk',       handle: 'lena',    avatar: 'LF', status: 'idle'    },
    { name: 'Jin Park',        handle: 'jin',     avatar: 'JP', status: 'idle'    },
    { name: 'Sara Khoury',     handle: 'sara',    avatar: 'SK', status: 'active'  },
    { name: 'Tom Beswick',     handle: 'tom',     avatar: 'TB', status: 'offline' },
  ];

  const harnesses = {
    'claude-code': { label: 'Claude Code', short: 'cc',  tint: 'oklch(0.68 0.14 38)'  },
    'codex':       { label: 'Codex',       short: 'cx',  tint: 'oklch(0.65 0.11 250)' },
    'opencode':    { label: 'OpenCode',    short: 'oc',  tint: 'oklch(0.62 0.12 145)' },
    'gemini':      { label: 'Gemini',      short: 'gm',  tint: 'oklch(0.68 0.10 295)' },
    'pi':          { label: 'Pi',          short: 'pi',  tint: 'oklch(0.70 0.11 75)'  },
  };

  // Sessions are the spine of the prototype.
  const sessions = [
    { id: 's_8f21', who: 'priya',  repo: 'iter/daemon',     branch: 'pr/socket-backpressure', task: 'Wire backpressure on Unix socket writes',         harness: 'claude-code', score: 92, dur: '14m',  startedAt: '10:42', day: 'Today',     tools: 47, accepted: 3, refinements: 1, status: 'landed',  files: 6, tests: '12/12', commits: 2 },
    { id: 's_8f1c', who: 'priya',  repo: 'iter/macapp',     branch: 'pr/menubar-poll',        task: 'Menubar dropdown — pause toggle + last-session row', harness: 'claude-code', score: 88, dur: '22m',  startedAt: '09:18', day: 'Today',     tools: 64, accepted: 4, refinements: 0, status: 'review',  files: 9, tests: '8/8',   commits: 3 },
    { id: 's_8f08', who: 'priya',  repo: 'iter/cli',        branch: 'pr/json-flag',           task: 'Add --json flag to skill.md invocation output',      harness: 'codex',       score: 74, dur: '08m',  startedAt: 'Yesterday 17:55', day: 'Yesterday', tools: 21, accepted: 2, refinements: 0, status: 'landed',  files: 3, tests: '5/5',   commits: 1 },
    { id: 's_8f02', who: 'priya',  repo: 'iter/daemon',     branch: 'pr/trace-flusher',       task: 'Fix race in trace flusher under high QPS',           harness: 'claude-code', score: 81, dur: '36m',  startedAt: 'Yesterday 14:11', day: 'Yesterday', tools: 92, accepted: 5, refinements: 2, status: 'landed',  files: 11, tests: '24/24', commits: 4 },
    { id: 's_8ef3', who: 'priya',  repo: 'iter/web',        branch: 'pr/tenant-confirm',      task: 'Tenant confirmation flow — auto-suggest on domain',  harness: 'opencode',    score: 67, dur: '19m',  startedAt: 'Yesterday 11:02', day: 'Yesterday', tools: 35, accepted: 1, refinements: 0, status: 'review',  files: 7, tests: '6/7',   commits: 2 },
    { id: 's_8ed8', who: 'priya',  repo: 'iter/daemon',     branch: 'pr/ws-reconnect',        task: 'Cloud WebSocket reconnect w/ exponential backoff',   harness: 'claude-code', score: 95, dur: '11m',  startedAt: 'Mon 16:30', day: 'Mon',       tools: 28, accepted: 2, refinements: 1, status: 'landed',  files: 4, tests: '14/14', commits: 1 },
    { id: 's_8ed2', who: 'priya',  repo: 'iter/macapp',     branch: 'pr/score-chip',          task: 'Score chip component + threshold colors',            harness: 'gemini',      score: 58, dur: '27m',  startedAt: 'Mon 13:08', day: 'Mon',       tools: 41, accepted: 0, refinements: 0, status: 'dropped', files: 5, tests: '3/6',   commits: 0 },
    { id: 's_8ec1', who: 'priya',  repo: 'iter/cli',        branch: 'pr/pretty-output',       task: 'Pretty text default output for `iter run`',          harness: 'claude-code', score: 86, dur: '15m',  startedAt: 'Fri 17:21', day: 'Fri',       tools: 33, accepted: 3, refinements: 1, status: 'landed',  files: 4, tests: '9/9',   commits: 2 },

    // Team activity (other people)
    { id: 's_8f24', who: 'mchen',  repo: 'iter/macapp',     branch: 'pr/onboarding-wizard',   task: 'Onboarding wizard — permission gate ordering',       harness: 'claude-code', score: 84, dur: '31m',  startedAt: '11:08', day: 'Today',     tools: 71, accepted: 6, refinements: 2, status: 'review',  files: 12, tests: '18/18', commits: 3 },
    { id: 's_8f1f', who: 'ana',    repo: 'iter/daemon',     branch: 'pr/ipc-protocol',        task: 'Unix socket IPC — length-prefixed frame protocol',   harness: 'codex',       score: 91, dur: '44m',  startedAt: '10:02', day: 'Today',     tools: 88, accepted: 4, refinements: 3, status: 'landed',  files: 14, tests: '22/22', commits: 5 },
    { id: 's_8f18', who: 'sara',   repo: 'iter/web',        branch: 'pr/admin-rbac',          task: 'iter.dev admin — RBAC for team admins',              harness: 'claude-code', score: 79, dur: '52m',  startedAt: '09:35', day: 'Today',     tools: 102, accepted: 7, refinements: 1, status: 'review',  files: 18, tests: '15/16', commits: 4 },
    { id: 's_8f0a', who: 'yusuf',  repo: 'iter/cli',        branch: 'pr/skill-md-parser',     task: 'skill.md frontmatter parser — strict mode',          harness: 'opencode',    score: 88, dur: '17m',  startedAt: 'Yesterday 16:44', day: 'Yesterday', tools: 39, accepted: 3, refinements: 1, status: 'landed',  files: 5, tests: '11/11', commits: 2 },
    { id: 's_8f04', who: 'lena',   repo: 'iter/macapp',     branch: 'pr/worktree-sandbox',    task: 'Stack simulation — open worktree in chosen dir',     harness: 'claude-code', score: 72, dur: '38m',  startedAt: 'Yesterday 14:55', day: 'Yesterday', tools: 56, accepted: 2, refinements: 0, status: 'review',  files: 9, tests: '7/9',   commits: 1 },
    { id: 's_8ef8', who: 'jin',    repo: 'iter/daemon',     branch: 'pr/score-pipeline',      task: 'Outcome scoring — derive from tests + commits',      harness: 'claude-code', score: 90, dur: '29m',  startedAt: 'Yesterday 11:30', day: 'Yesterday', tools: 67, accepted: 5, refinements: 2, status: 'landed',  files: 8, tests: '20/20', commits: 3 },
    { id: 's_8ed5', who: 'mchen',  repo: 'iter/web',        branch: 'pr/billing-gate',        task: 'Billing — gracefully degrade on Stripe outage',      harness: 'gemini',      score: 64, dur: '23m',  startedAt: 'Mon 15:12', day: 'Mon',       tools: 44, accepted: 1, refinements: 0, status: 'dropped', files: 6, tests: '4/6',   commits: 0 },
  ];

  // The "trace" of a single session for the detail view.
  const sessionTrace = [
    { t: '00:00', kind: 'start',   label: 'Session started',           detail: 'harness=claude-code · branch=pr/socket-backpressure'  },
    { t: '00:02', kind: 'prompt',  label: 'You',                       detail: 'Add backpressure on the Unix socket writer so the daemon doesn\u2019t buffer unbounded when the Mac app is paused.' },
    { t: '00:04', kind: 'suggest', label: 'Suggested refinement',      detail: 'From Ana — "Specify the high-water mark in bytes, not messages. We hit this in pr/ipc-protocol."', accepted: true },
    { t: '00:08', kind: 'tool',    label: 'read_file',                 detail: 'daemon/socket/writer.go' },
    { t: '00:14', kind: 'tool',    label: 'grep',                      detail: 'pattern=\"unbounded\\|bufio.NewWriter\"' },
    { t: '00:31', kind: 'subagent',label: 'subagent · trace-inspector', detail: '12 tool calls · expand', children: 12 },
    { t: '02:18', kind: 'tool',    label: 'edit_file',                 detail: 'daemon/socket/writer.go +84 \u221218' },
    { t: '03:04', kind: 'tool',    label: 'run_tests',                 detail: 'go test ./daemon/socket/... \u2192 ok' },
    { t: '05:22', kind: 'tool',    label: 'edit_file',                 detail: 'daemon/socket/writer_test.go +56' },
    { t: '07:11', kind: 'tool',    label: 'run_tests',                 detail: 'go test ./... \u2192 12/12' },
    { t: '09:48', kind: 'tool',    label: 'edit_file',                 detail: 'daemon/socket/config.go +22' },
    { t: '11:30', kind: 'commit',  label: 'commit · 4f2a1c8',          detail: 'socket: bounded writer with 1MiB high-water mark' },
    { t: '12:01', kind: 'commit',  label: 'commit · 8c7b912',          detail: 'socket: tests for backpressure under pause' },
    { t: '13:42', kind: 'extract', label: 'Refinement extracted',      detail: 'When implementing backpressure: specify thresholds in bytes; surface pause state via socket-level signal, not application heartbeat.' },
    { t: '14:08', kind: 'end',     label: 'Session ended',             detail: 'score=92 · landed · 2 commits · 12/12 tests' },
  ];

  const refinementsContributed = [
    { id: 'r_42', title: 'Always specify socket thresholds in bytes, not messages',  used: 6, age: '2d' },
    { id: 'r_38', title: 'For trace flushers under high QPS, use a sharded ring',     used: 11, age: '5d' },
    { id: 'r_31', title: 'Tenant confirmation: defer until macOS permissions granted', used: 4, age: '1w' },
    { id: 'r_27', title: 'Score chip thresholds: 0\u201359 red, 60\u201379 amber, 80+ green', used: 8, age: '1w' },
  ];

  const suggestionsWaiting = [
    { id: 'sg_11', from: 'ana',   title: 'Length-prefix your frames; magic-byte sniff is brittle', context: 'iter/daemon · ipc' },
    { id: 'sg_09', from: 'jin',   title: 'Memoize the score derivation \u2014 it\u2019s O(n) per fetch',    context: 'iter/macapp · scoring' },
  ];

  const activeStack = {
    name: 'iter · core',
    harnesses: ['claude-code', 'codex', 'opencode'],
    skills: ['frontend-design', 'rust-async', 'go-test-runner', 'sql-migrator'],
    docs: ['CLAUDE.md', 'AGENTS.md', 'docs/architecture.md'],
    notes: 4,
  };

  return { me, teammates, harnesses, sessions, sessionTrace, refinementsContributed, suggestionsWaiting, activeStack };
})();
