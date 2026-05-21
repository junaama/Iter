// iter — main app shell

const TWEAK_DEFAULTS = /*EDITMODE-BEGIN*/{
  "dark": false,
  "layout": "table",
  "density": "regular",
  "accent": "#D8623A"
}/*EDITMODE-END*/;

function App() {
  const data = window.ITER_DATA;
  const [t, setTweak] = useTweaks(TWEAK_DEFAULTS);
  const [view, setView] = React.useState('me');         // me | team | sessions | stack | suggest | settings | session
  const [selectedSessionId, setSelectedSessionId] = React.useState(null);
  const [openSessionId, setOpenSessionId] = React.useState(null);
  const [range, setRange] = React.useState('7d');
  const [tabHover, setTabHover] = React.useState(null);

  // Apply theme on root
  React.useEffect(() => {
    document.documentElement.dataset.theme = t.dark ? 'dark' : 'light';
  }, [t.dark]);

  React.useEffect(() => {
    document.documentElement.style.setProperty('--accent', t.accent);
  }, [t.accent]);

  const openSession = (id) => { setOpenSessionId(id); setView('session'); };
  const goBack = () => { setOpenSessionId(null); setView('me'); };

  const session = data.sessions.find(s => s.id === openSessionId);

  const headerTitle = view === 'me' ? 'Me'
                    : view === 'team' ? 'Team'
                    : view === 'sessions' ? 'Sessions'
                    : view === 'stack' ? 'Stack'
                    : view === 'suggest' ? 'Suggestions'
                    : view === 'settings' ? 'Settings'
                    : view === 'session' ? 'Session'
                    : '';

  const showRail = view === 'me' || view === 'team' || view === 'session';

  return (
    <div className="app-stage">
      <div className="mac-window">

        {/* Title bar */}
        <div className="titlebar">
          <div className="traffic">
            <span className="tl-red"></span>
            <span className="tl-yellow"></span>
            <span className="tl-green"></span>
          </div>
          <div className="titlebar-title">iter — {headerTitle}</div>
          <div className="titlebar-right">
            <span className="titlebar-pill">
              <span className="dot"></span>
              daemon · 5s
            </span>
            <span className="titlebar-pill">
              ⌘K
            </span>
          </div>
        </div>

        <div className="body-split">
          <Sidebar
            view={view}
            setView={(v) => { setView(v); if (v !== 'session') setOpenSessionId(null); }}
            data={data}
            openSession={openSession}
          />

          <div className="main">
            {/* Subbar with crumbs + view tabs + search */}
            <div className="subbar">
              {view === 'session' && session ? (
                <div className="crumbs">
                  <span style={{ cursor: 'default' }} onClick={goBack}>Me</span>
                  <span className="sep">/</span>
                  <span className="mono">{session.repo}</span>
                  <span className="sep">/</span>
                  <span className="cur">{session.id}</span>
                </div>
              ) : (
                <div className="tabs">
                  <button className={`tab ${view === 'me' ? 'on' : ''}`} onClick={() => setView('me')}>
                    Me <span className="ct">47</span>
                  </button>
                  <button className={`tab ${view === 'team' ? 'on' : ''}`} onClick={() => setView('team')}>
                    Team <span className="ct">132</span>
                  </button>
                </div>
              )}

              <div className="subbar-right">
                {(view === 'me' || view === 'team') && (
                  <div className="segmented" title="Time range">
                    {['24h','7d','30d','all'].map(r => (
                      <button key={r}
                        className={range === r ? 'on' : ''}
                        onClick={() => setRange(r)}>{r}</button>
                    ))}
                  </div>
                )}
                {(view === 'me' || view === 'team') && (
                  <div className="segmented" title="Layout">
                    {[
                      ['table','Table'],
                      ['cards','Cards'],
                      ['feed','Feed'],
                    ].map(([k,l]) => (
                      <button key={k}
                        className={t.layout === k ? 'on' : ''}
                        onClick={() => setTweak('layout', k)}>{l}</button>
                    ))}
                  </div>
                )}
                <div className="search-wrap">
                  <span className="ico"><Icon.Search /></span>
                  <input className="search-input" placeholder="Search sessions, refinements…" />
                  <span className="kbd">⌘K</span>
                </div>
              </div>
            </div>

            {/* Content area */}
            <div className={`content ${showRail ? '' : 'no-rail'}`}>
              <div className="main-pane">
                {view === 'me' && (
                  <MeView data={data} layout={t.layout}
                    selectedId={selectedSessionId}
                    onSelect={(id) => openSession(id)}
                    range={range} setRange={setRange} />
                )}
                {view === 'team' && (
                  <TeamView data={data} layout={t.layout}
                    selectedId={selectedSessionId}
                    onSelect={(id) => openSession(id)} />
                )}
                {view === 'session' && (
                  <SessionView session={session} data={data} onBack={goBack} />
                )}
                {(view !== 'me' && view !== 'team' && view !== 'session') && (
                  <EmptyView view={view} />
                )}
              </div>

              {showRail && (
                <div className="rail">
                  {view === 'me'      && <MeRail      data={data} openSession={openSession} />}
                  {view === 'team'    && <TeamRail    data={data} />}
                  {view === 'session' && <SessionRail session={session} data={data} />}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>

      <TweaksPanel>
        <TweakSection label="Theme" />
        <TweakToggle label="Dark mode" value={t.dark}
          onChange={(v) => setTweak('dark', v)} />
        <TweakColor label="Accent" value={t.accent}
          options={['#D8623A', '#3D6CC9', '#3F9166', '#9E4DD2']}
          onChange={(v) => setTweak('accent', v)} />

        <TweakSection label="Layout" />
        <TweakRadio label="Sessions layout" value={t.layout}
          options={['table', 'cards', 'feed']}
          onChange={(v) => setTweak('layout', v)} />

        <TweakSection label="Navigate" />
        <TweakButton label="Open a session" onClick={() => openSession('s_8f21')}>
          → Backpressure session
        </TweakButton>
        <TweakButton label="Jump to view" onClick={() => setView('me')}>
          Me
        </TweakButton>
        <TweakButton onClick={() => setView('team')}>
          Team
        </TweakButton>
      </TweaksPanel>
    </div>
  );
}

function EmptyView({ view }) {
  const copy = {
    sessions: { h: 'All sessions', s: 'Full sessions browser — filter by harness, repo, score, refinement count.' },
    stack:    { h: 'Stack', s: 'Active stack pills, simulate teammate (read-only), open worktree in a chosen directory.' },
    suggest:  { h: 'Suggestions', s: '2 suggestions waiting from your teammates. Open Me to triage.' },
    settings: { h: 'Settings', s: 'Permissions, harness paths, tenant, telemetry. Admin/billing on iter.dev.' },
  }[view] || { h: '', s: '' };
  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center',
      color: 'var(--t2)', padding: 80, textAlign: 'center',
      gap: 8
    }}>
      <div style={{
        fontSize: 11, color: 'var(--t3)', textTransform: 'uppercase', letterSpacing: '0.1em',
        fontFamily: 'var(--font-mono)'
      }}>not in v1 scope · prototype stub</div>
      <h2 style={{ margin: 0, color: 'var(--t1)', fontSize: 18, fontWeight: 600 }}>{copy.h}</h2>
      <div style={{ maxWidth: 420 }}>{copy.s}</div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById('root')).render(<App />);
