// Me / Team / Session detail views

// ─────────── Shared session row renderers ───────────

function SessionTable({ rows, selectedId, onSelect, data, showWho = false }) {
  return (
    <div className="table">
      <div className="thead" style={showWho ? null : null}>
        <span>Started</span>
        <span>{showWho ? 'Author · Repo · Task' : 'Repo · Task'}</span>
        <span>Harness</span>
        <span style={{ textAlign: 'right' }}>Dur</span>
        <span style={{ textAlign: 'center' }}>Score</span>
        <span>Status</span>
        <span style={{ textAlign: 'right' }}>Accepted</span>
      </div>
      {rows.map(s => (
        <div key={s.id}
          className={`trow ${selectedId === s.id ? 'sel' : ''}`}
          onClick={() => onSelect(s.id)}>
          <span className="when">{s.startedAt}</span>
          <span className="who">
            {showWho ? <Avatar handle={s.who} /> : null}
            <span className="repo nowrap">{s.repo}</span>
            <span className="t3">·</span>
            <span className="task">{s.task}</span>
          </span>
          <Harness id={s.harness} data={data} />
          <span className="dur">{s.dur}</span>
          <span style={{ textAlign: 'center' }}><Score value={s.score}/></span>
          <StatusChip status={s.status} />
          <span className="accepted" style={{ textAlign: 'right' }}>
            <span className="t1">{s.accepted}</span>
            <span className="t3"> / {s.tools}</span>
          </span>
        </div>
      ))}
    </div>
  );
}

function SessionCards({ rows, selectedId, onSelect, data }) {
  return (
    <div className="cards-grid">
      {rows.map(s => {
        const bars = Array.from({ length: 18 }, (_, i) =>
          4 + Math.round(10 * Math.abs(Math.sin(i * 1.3 + s.score)))
        );
        return (
          <div key={s.id}
            className={`scard ${selectedId === s.id ? 'sel' : ''}`}
            onClick={() => onSelect(s.id)}>
            <div className="row1">
              <span className="repo">{s.repo}</span>
              <span className="spacer"></span>
              <Score value={s.score} />
            </div>
            <div className="task">{s.task}</div>
            <div className="meta">
              <Harness id={s.harness} data={data} />
              <span className="sep">·</span>
              <span>{s.dur}</span>
              <span className="sep">·</span>
              <span>{s.tools} tools</span>
              <span className="sep">·</span>
              <span>{s.accepted} accepted</span>
            </div>
            <div className="bars">
              {bars.map((h, i) => (
                <span key={i} style={{
                  height: h,
                  background: i < bars.length * (s.score/100)
                    ? data.harnesses[s.harness].tint
                    : null
                }}/>
              ))}
            </div>
            <div className="meta" style={{ marginTop: 2 }}>
              <StatusChip status={s.status} />
              <span className="spacer"></span>
              <span className="t3">{s.startedAt}</span>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function SessionFeed({ rows, selectedId, onSelect, data, showWho = false }) {
  // Group by day field
  const grouped = [];
  let curDay = null;
  rows.forEach(s => {
    if (s.day !== curDay) { grouped.push({ day: s.day, items: [] }); curDay = s.day; }
    grouped[grouped.length - 1].items.push(s);
  });
  return (
    <div className="feed">
      {grouped.map(g => (
        <React.Fragment key={g.day}>
          <div className="feed-day">{g.day}</div>
          {g.items.map(s => (
            <div key={s.id}
              className={`fitem ${selectedId === s.id ? 'sel' : ''}`}
              onClick={() => onSelect(s.id)}>
              <span className="when">{s.startedAt.split(' ').pop()}</span>
              <Avatar handle={s.who} />
              <div className="body">
                <div className="b1">
                  <span className="task">{s.task}</span>
                  <span className="repo">{s.repo}</span>
                </div>
                <div className="b2">
                  <Harness id={s.harness} data={data} />
                  <span className="sep">·</span>
                  <span>{s.dur}</span>
                  <span className="sep">·</span>
                  <span>{s.tools} tools · {s.accepted} accepted</span>
                  {s.refinements > 0 ? (<>
                    <span className="sep">·</span>
                    <span style={{ color: 'var(--accent)' }}>+{s.refinements} refinement{s.refinements>1?'s':''}</span>
                  </>) : null}
                </div>
              </div>
              <div className="right">
                <Score value={s.score} />
                <StatusChip status={s.status} />
              </div>
            </div>
          ))}
        </React.Fragment>
      ))}
    </div>
  );
}

// ─────────── Me dashboard ───────────

function MeView({ data, layout, selectedId, onSelect, range, setRange }) {
  const myRows = data.sessions.filter(s => s.who === 'priya');

  const spark = (n) => Array.from({ length: n }, (_, i) => 0.3 + 0.5 * Math.abs(Math.sin(i * 0.9)));

  return (
    <>
      <div className="kpi-row">
        <KPI label="Sessions"   value="47"        delta="+9"      deltaDir="up"  spark={spark(14)} />
        <KPI label="Acceptance" value="68"  unit="%"  delta="+4"  deltaDir="up"  spark={spark(14)} sparkAccent />
        <KPI label="Avg score"  value="81"        delta="+3"      deltaDir="up"  spark={spark(14)} />
        <KPI label="Time saved" value="4h 12m"    delta="−18m"    deltaDir="dn"  spark={spark(14)} />
      </div>

      <div className="section">
        <div className="section-h">
          <h2>Recent sessions</h2>
          <span className="ct">{myRows.length} this {range}</span>
          <div className="right">
            <button className="iconbtn" title="Filter"><Icon.Filter /></button>
            <button className="iconbtn" title="Refresh"><Icon.Refresh /></button>
          </div>
        </div>

        {layout === 'table' && (
          <SessionTable rows={myRows} selectedId={selectedId} onSelect={onSelect} data={data} />
        )}
        {layout === 'cards' && (
          <SessionCards rows={myRows} selectedId={selectedId} onSelect={onSelect} data={data} />
        )}
        {layout === 'feed' && (
          <SessionFeed rows={myRows} selectedId={selectedId} onSelect={onSelect} data={data} />
        )}
      </div>
    </>
  );
}

function MeRail({ data, openSession }) {
  return (
    <>
      <div className="rail-card">
        <h3>Refinements you contributed <span className="ct">{data.refinementsContributed.length}</span></h3>
        {data.refinementsContributed.map(r => (
          <div key={r.id} className="rail-item">
            <div className="t">{r.title}</div>
            <div className="m">
              <span className="mono">{r.id}</span>
              <span className="sep">·</span>
              <span>used {r.used}× by team</span>
              <span className="sep">·</span>
              <span>{r.age}</span>
            </div>
          </div>
        ))}
      </div>

      <div className="rail-card">
        <h3>Suggestions waiting <span className="ct">{data.suggestionsWaiting.length}</span></h3>
        {data.suggestionsWaiting.map(s => (
          <div key={s.id} className="rail-item">
            <div className="t">{s.title}</div>
            <div className="m">
              <Avatar handle={s.from} />
              <span>{data.teammates.find(t => t.handle === s.from)?.name.split(' ')[0]}</span>
              <span className="sep">·</span>
              <span>{s.context}</span>
            </div>
            <div className="actions">
              <button className="btn primary">Copy to clipboard<span className="kbd">⌘C</span></button>
              <button className="btn">Dismiss</button>
            </div>
          </div>
        ))}
      </div>

      <div className="rail-card">
        <h3>Active stack <span className="ct mono">{data.activeStack.name}</span></h3>
        <div className="rail-item">
          <div className="m" style={{ marginBottom: 4 }}>HARNESSES</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {data.activeStack.harnesses.map(h => (
              <span key={h} className="stackpill">{data.harnesses[h].label}</span>
            ))}
          </div>
        </div>
        <div className="rail-item">
          <div className="m" style={{ marginBottom: 4 }}>SKILLS · {data.activeStack.skills.length}</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {data.activeStack.skills.map(s => (
              <span key={s} className="stackpill">{s}</span>
            ))}
          </div>
        </div>
        <div className="rail-item">
          <div className="m" style={{ marginBottom: 4 }}>DOCS · NOTES</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {data.activeStack.docs.map(d => <span key={d} className="stackpill">{d}</span>)}
            <span className="stackpill">+{data.activeStack.notes} notes</span>
          </div>
        </div>
      </div>
    </>
  );
}

// ─────────── Team dashboard ───────────

function TeamView({ data, layout, selectedId, onSelect }) {
  const teamRows = data.sessions; // everyone

  // Contributors leaderboard
  const contribByHandle = {};
  data.sessions.forEach(s => {
    const c = contribByHandle[s.who] || (contribByHandle[s.who] = { handle: s.who, sessions: 0, refinements: 0, accepted: 0, score: 0 });
    c.sessions += 1;
    c.refinements += s.refinements;
    c.accepted += s.accepted;
    c.score += s.score;
  });
  Object.values(contribByHandle).forEach(c => { c.avg = Math.round(c.score / c.sessions); });
  const board = Object.values(contribByHandle).sort((a,b) => b.refinements - a.refinements || b.sessions - a.sessions);
  const maxRef = Math.max(...board.map(b => b.refinements), 1);

  const spark = (n, seed=1) => Array.from({ length: n }, (_, i) => 0.3 + 0.55 * Math.abs(Math.sin(i * 0.7 + seed)));

  return (
    <>
      <div className="kpi-row">
        <KPI label="Team sessions"      value="132"      delta="+18"  deltaDir="up" spark={spark(14,1)} />
        <KPI label="Refinements shared" value="24"       delta="+6"   deltaDir="up" spark={spark(14,2)} sparkAccent />
        <KPI label="Avg team score"     value="83"       delta="+2"   deltaDir="up" spark={spark(14,3)} />
        <KPI label="Active now"         value="4" unit="/ 8" delta="2 paused" deltaDir="flat" spark={spark(14,4)} />
      </div>

      <div className="team-grid">
        <div className="boxed">
          <div className="boxed-h">
            Contributors
            <span className="ct">by refinements · 7d</span>
            <span className="right">view all</span>
          </div>
          {board.map((c, i) => {
            const tm = data.teammates.find(t => t.handle === c.handle);
            return (
              <div key={c.handle} className="member-row">
                <Avatar handle={c.handle} size="lg" />
                <div className="name">
                  <span className="n">{tm?.name || c.handle}</span>
                  <span className="s">{c.sessions} sessions · avg {c.avg}</span>
                </div>
                <span className="num">{c.refinements}</span>
                <span className="num t3">{c.accepted}</span>
                <span className="bar">
                  <span className="track">
                    <span className="fill" style={{ width: `${(c.refinements/maxRef)*100}%` }}></span>
                  </span>
                </span>
              </div>
            );
          })}
          <div className="member-row" style={{ background: 'var(--sidebar)', color: 'var(--t3)', fontSize: 10.5, textTransform: 'uppercase', letterSpacing: '0.06em', height: 24 }}>
            <span></span>
            <span></span>
            <span className="num t3" style={{ fontSize: 10 }}>REFINEMENTS</span>
            <span className="num t3" style={{ fontSize: 10 }}>ACCEPTED</span>
            <span></span>
          </div>
        </div>

        <div className="boxed">
          <div className="boxed-h">
            Top circulating suggestions
            <span className="ct">7d</span>
            <span className="right">view all</span>
          </div>
          {[
            { id: 'r_38', title: 'For trace flushers under high QPS, use a sharded ring', by: 'priya',  used: 11, repos: 3 },
            { id: 'r_44', title: 'Length-prefix your IPC frames; magic-byte sniff is brittle', by: 'ana', used: 9, repos: 2 },
            { id: 'r_42', title: 'Always specify socket thresholds in bytes, not messages', by: 'priya',  used: 6, repos: 2 },
            { id: 'r_47', title: 'On SwiftUI menubar polling, dedupe by daemon clock', by: 'mchen', used: 5, repos: 1 },
            { id: 'r_36', title: 'Tenant confirm: defer until macOS permissions granted', by: 'priya', used: 4, repos: 1 },
            { id: 'r_49', title: 'Cloud WS reconnect: jitter the backoff or you thunder', by: 'jin', used: 4, repos: 2 },
          ].map(r => (
            <div key={r.id} className="member-row" style={{ gridTemplateColumns: '20px minmax(0,1fr) 64px 60px 28px' }}>
              <Avatar handle={r.by} />
              <div className="name">
                <span className="n" style={{
                  fontWeight: 400,
                  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap'
                }}>{r.title}</span>
                <span className="s mono">{r.id} · {data.teammates.find(t => t.handle === r.by)?.name.split(' ')[0]}</span>
              </div>
              <span className="num">{r.used}×</span>
              <span className="num t3">{r.repos} repos</span>
              <span className="t3"><Icon.ChevR /></span>
            </div>
          ))}
        </div>
      </div>

      <div className="section">
        <div className="section-h">
          <h2>Team sessions</h2>
          <span className="ct">{teamRows.length} this 7d</span>
          <div className="right">
            <button className="iconbtn" title="Filter"><Icon.Filter /></button>
            <button className="iconbtn" title="Refresh"><Icon.Refresh /></button>
          </div>
        </div>
        {layout === 'table' && <SessionTable rows={teamRows} selectedId={selectedId} onSelect={onSelect} data={data} showWho />}
        {layout === 'cards' && <SessionCards rows={teamRows} selectedId={selectedId} onSelect={onSelect} data={data} />}
        {layout === 'feed'  && <SessionFeed  rows={teamRows} selectedId={selectedId} onSelect={onSelect} data={data} showWho />}
      </div>
    </>
  );
}

function TeamRail({ data }) {
  return (
    <>
      <div className="rail-card">
        <h3>Active now <span className="ct">{data.teammates.filter(t => t.status === 'active').length}</span></h3>
        {data.teammates.map(t => (
          <div key={t.handle} className="rail-item" style={{
            display: 'grid',
            gridTemplateColumns: '28px minmax(0,1fr) auto',
            alignItems: 'center',
            gap: 10,
            paddingTop: 8, paddingBottom: 8
          }}>
            <Avatar handle={t.handle} size="lg" />
            <div style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.25 }}>
              <span style={{ color: 'var(--t1)', fontSize: 12 }}>{t.name}</span>
              <span className="mono" style={{ fontSize: 10.5, color: 'var(--t3)' }}>
                {t.status === 'active'  ? 'in session · 8m' :
                 t.status === 'paused'  ? 'paused' :
                 t.status === 'idle'    ? 'idle 1h' :
                                          'offline'}
              </span>
            </div>
            <span className={`status-chip ${
              t.status === 'active' ? 'landed' :
              t.status === 'paused' ? 'review' : 'dropped'
            }`}>
              <span className="d"></span>
            </span>
          </div>
        ))}
      </div>

      <div className="rail-card">
        <h3>Stacks in use <span className="ct">3</span></h3>
        <div className="rail-item">
          <div className="t">iter · core</div>
          <div className="m"><span>5 members</span><span className="sep">·</span><span>cc, cx, oc</span></div>
        </div>
        <div className="rail-item">
          <div className="t">iter · web</div>
          <div className="m"><span>2 members</span><span className="sep">·</span><span>cc, gm</span></div>
        </div>
        <div className="rail-item">
          <div className="t">iter · cli</div>
          <div className="m"><span>1 member</span><span className="sep">·</span><span>cx</span></div>
        </div>
      </div>
    </>
  );
}

// ─────────── Session detail ───────────

function SessionView({ session, data, onBack }) {
  if (!session) return null;
  return (
    <>
      <div className="section">
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <button className="iconbtn" onClick={onBack} title="Back"><Icon.Back /></button>
          <span className="mono" style={{ fontSize: 11, color: 'var(--t3)' }}>{session.id}</span>
          <span style={{ color: 'var(--t4)' }}>·</span>
          <span className="mono" style={{ fontSize: 11, color: 'var(--t2)' }}>{session.repo}</span>
          <span style={{ color: 'var(--t4)' }}>·</span>
          <span className="mono" style={{ fontSize: 11, color: 'var(--t2)' }}>{session.branch}</span>
          <span className="spacer"></span>
          <button className="btn">Open worktree</button>
          <button className="btn">Share stack</button>
        </div>
        <h1 style={{ margin: '6px 0 8px', fontSize: 20, fontWeight: 600, letterSpacing: '-0.012em', color: 'var(--t1)' }}>
          {session.task}
        </h1>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, color: 'var(--t2)', fontFamily: 'var(--font-mono)', fontSize: 11.5 }}>
          <Avatar handle={session.who} />
          <span>{data.teammates.find(t => t.handle === session.who)?.name}</span>
          <span style={{ color: 'var(--t4)' }}>·</span>
          <Harness id={session.harness} data={data} />
          <span style={{ color: 'var(--t4)' }}>·</span>
          <span>{session.startedAt}</span>
          <span style={{ color: 'var(--t4)' }}>·</span>
          <span>{session.dur}</span>
          <span style={{ color: 'var(--t4)' }}>·</span>
          <StatusChip status={session.status} />
          <span className="spacer"></span>
          <Score value={session.score} />
        </div>
      </div>

      <div className="outcome">
        <div><span className="l">Tests</span><span className="v">{session.tests}</span></div>
        <div><span className="l">Commits</span><span className="v">{session.commits}</span></div>
        <div><span className="l">Files changed</span><span className="v">{session.files}</span></div>
        <div><span className="l">Tool calls</span><span className="v">{session.tools}</span></div>
      </div>

      <div className="section">
        <div className="section-h">
          <h2>Trace</h2>
          <span className="ct">{data.sessionTrace.length} events</span>
          <div className="right">
            <div className="segmented">
              <button className="on">All</button>
              <button>Tools</button>
              <button>Prompts</button>
            </div>
          </div>
        </div>
        <div className="boxed">
          <div className="trace">
            {data.sessionTrace.map((ev, i) => (
              <div key={i} className="trace-row">
                <span className="t">{ev.t}</span>
                <span className={`k ${ev.kind}`}>{ev.kind}</span>
                <div className="body">
                  <span className="l">{ev.label}</span>
                  {ev.detail ? <span className="d">{ev.detail}</span> : null}
                  {ev.kind === 'subagent' && ev.children ? (
                    <span className="mono" style={{ fontSize: 10.5, color: 'var(--t3)', marginTop: 2 }}>
                      ▸ {ev.children} child tool calls (click to expand)
                    </span>
                  ) : null}
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </>
  );
}

function SessionRail({ session, data }) {
  if (!session) return null;
  return (
    <>
      <div className="rail-card">
        <h3>Refinement extracted</h3>
        <div className="rail-item">
          <div className="t" style={{ fontStyle: 'italic' }}>
            "When implementing backpressure: specify thresholds in bytes; surface pause state via socket-level signal, not application heartbeat."
          </div>
          <div className="m" style={{ marginTop: 6 }}>
            <span className="mono">r_52</span>
            <span className="sep">·</span>
            <span>scoped to: socket, IPC</span>
          </div>
          <div className="actions">
            <button className="btn primary">Share with team</button>
            <button className="btn">Edit</button>
          </div>
        </div>
      </div>

      <div className="rail-card">
        <h3>Suggestions surfaced <span className="ct">3 / 4 accepted</span></h3>
        {[
          { from: 'ana', t: 'Specify the high-water mark in bytes, not messages', accepted: true },
          { from: 'jin', t: 'Use channel send-with-deadline, not blocking write',  accepted: true },
          { from: 'mchen', t: 'Surface backpressure as a daemon-level event for the menubar to show', accepted: true },
          { from: 'lena', t: 'Add a metric for time-spent-blocked-on-write',     accepted: false },
        ].map((s, i) => (
          <div key={i} className="rail-item">
            <div className="t">{s.t}</div>
            <div className="m" style={{ marginTop: 2 }}>
              <Avatar handle={s.from} />
              <span>{data.teammates.find(t => t.handle === s.from)?.name.split(' ')[0]}</span>
              <span className="sep">·</span>
              <span style={{ color: s.accepted ? 'var(--good)' : 'var(--t3)' }}>
                {s.accepted ? 'accepted' : 'dismissed'}
              </span>
            </div>
          </div>
        ))}
      </div>

      <div className="rail-card">
        <h3>Files touched</h3>
        {[
          'daemon/socket/writer.go',
          'daemon/socket/writer_test.go',
          'daemon/socket/config.go',
          'daemon/internal/metrics.go',
          'docs/architecture.md',
          'CHANGELOG.md',
        ].map((f, i) => (
          <div key={i} className="rail-item" style={{ padding: '6px 12px' }}>
            <div className="m">
              <span className="mono" style={{ color: 'var(--t1)' }}>{f}</span>
              <span className="spacer"></span>
              <span style={{ color: 'var(--good)' }}>+{(i+1)*14}</span>
              <span style={{ color: 'var(--bad)' }}>−{i*7}</span>
            </div>
          </div>
        ))}
      </div>
    </>
  );
}

Object.assign(window, { MeView, MeRail, TeamView, TeamRail, SessionView, SessionRail });
