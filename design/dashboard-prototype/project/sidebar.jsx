// Sidebar nav for iter dashboard

function Sidebar({ view, setView, data, openSession }) {
  const items = [
    { id: 'me',       label: 'Me',          icon: <Icon.Me />,       count: 47,  kbd: '⌘1' },
    { id: 'team',     label: 'Team',        icon: <Icon.Team />,     count: 132, kbd: '⌘2' },
    { id: 'sessions', label: 'Sessions',    icon: <Icon.Sessions />, count: 'all', kbd: '⌘3' },
    { id: 'stack',    label: 'Stack',       icon: <Icon.Stack />,             kbd: '⌘4' },
    { id: 'suggest',  label: 'Suggestions', icon: <Icon.Suggest />,  count: 2 },
    { id: 'settings', label: 'Settings',    icon: <Icon.Settings />,          kbd: '⌘,' },
  ];

  const recent = data.sessions.filter(s => s.who === 'priya').slice(0, 4);

  return (
    <aside className="sidebar">
      <div className="side-section" style={{ paddingTop: 0 }}>
        <div className="workspace">
          <span className="ws-mark">i</span>
          <span className="ws-name">
            <span className="n">iter · core</span>
            <span className="s">priya@iter.dev</span>
          </span>
          <span className="chev">⌄</span>
        </div>
      </div>

      <div className="navlist">
        {items.slice(0, 4).map(it => (
          <button key={it.id}
            className={`navitem ${view.startsWith(it.id) ? 'active' : ''}`}
            onClick={() => setView(it.id)}>
            <span className="ico">{it.icon}</span>
            <span>{it.label}</span>
            {it.count !== undefined ? <span className="count">{it.count}</span> : null}
          </button>
        ))}
      </div>

      <div className="side-section">
        <div className="heading">
          <span>Active stack</span>
          <span className="plus">edit</span>
        </div>
      </div>
      <div className="stackpills">
        {data.activeStack.harnesses.map(h => (
          <span key={h} className="stackpill">{data.harnesses[h].label}</span>
        ))}
        {data.activeStack.skills.slice(0, 2).map(s => (
          <span key={s} className="stackpill">+{s}</span>
        ))}
        <span className="stackpill">+{data.activeStack.skills.length - 2} skills</span>
      </div>

      <div className="side-section">
        <div className="heading">
          <span>Recent</span>
        </div>
      </div>
      <div className="navlist" style={{ padding: '0 6px 6px' }}>
        {recent.map(s => (
          <button key={s.id}
            className="navitem"
            onClick={() => openSession(s.id)}>
            <span className="ico" style={{ width: 14 }}>
              <span style={{
                display: 'inline-block', width: 5, height: 5, borderRadius: 1,
                background: data.harnesses[s.harness].tint
              }}/>
            </span>
            <span style={{
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              fontSize: 11.5
            }}>{s.task}</span>
          </button>
        ))}
      </div>

      <div className="navlist" style={{ marginTop: 'auto' }}>
        {items.slice(4).map(it => (
          <button key={it.id}
            className={`navitem ${view === it.id ? 'active' : ''}`}
            onClick={() => setView(it.id)}>
            <span className="ico">{it.icon}</span>
            <span>{it.label}</span>
            {it.count !== undefined ? <span className="count">{it.count}</span> : null}
          </button>
        ))}
      </div>

      <div className="side-foot">
        <div className="row">
          <span><span className="dot"></span>daemon · running</span>
          <span>1.4.2</span>
        </div>
        <div className="row">
          <span>last sync</span>
          <span>3s ago</span>
        </div>
      </div>
    </aside>
  );
}

window.Sidebar = Sidebar;
