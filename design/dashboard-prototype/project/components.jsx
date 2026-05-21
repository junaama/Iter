// Shared small components: icons, avatars, score chips, KPI, sparklines, etc.

const Icon = {
  Me: (p) => (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" {...p}>
      <circle cx="7" cy="5" r="2.4" stroke="currentColor" strokeWidth="1.2"/>
      <path d="M2.5 12c.6-2.2 2.4-3.3 4.5-3.3S10.9 9.8 11.5 12" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
    </svg>
  ),
  Team: (p) => (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" {...p}>
      <circle cx="4.7" cy="5.3" r="1.9" stroke="currentColor" strokeWidth="1.2"/>
      <circle cx="9.5" cy="5.3" r="1.9" stroke="currentColor" strokeWidth="1.2"/>
      <path d="M1.5 11.6c.5-1.6 1.8-2.5 3.2-2.5s2.7.9 3.2 2.5M6.3 11.6c.5-1.6 1.8-2.5 3.2-2.5s2.7.9 3.2 2.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
    </svg>
  ),
  Sessions: (p) => (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" {...p}>
      <rect x="2" y="3" width="10" height="8" rx="1.2" stroke="currentColor" strokeWidth="1.2"/>
      <path d="M4.5 6.3l1.6 1.6L9 5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  ),
  Stack: (p) => (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" {...p}>
      <path d="M7 1.6 1.6 4 7 6.4 12.4 4 7 1.6Z" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round"/>
      <path d="m1.6 7 5.4 2.4L12.4 7M1.6 10l5.4 2.4 5.4-2.4" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round"/>
    </svg>
  ),
  Suggest: (p) => (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" {...p}>
      <path d="M7 1.5v1.4M11.9 3.3l-1 1M12.5 7h-1.4M3.1 4.3l-1-1M2.9 7H1.5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
      <path d="M4.6 7a2.4 2.4 0 1 1 3.8 1.9c-.5.4-.8.9-.8 1.5v.4H6.2v-.4c0-.6-.3-1.1-.8-1.5A2.4 2.4 0 0 1 4.6 7Z" stroke="currentColor" strokeWidth="1.2"/>
      <path d="M5.8 12.4h2.4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
    </svg>
  ),
  Settings: (p) => (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" {...p}>
      <circle cx="7" cy="7" r="1.6" stroke="currentColor" strokeWidth="1.2"/>
      <path d="M7 1.5v1.6M7 10.9v1.6M12.5 7h-1.6M3.1 7H1.5M10.9 3.1 9.8 4.2M4.2 9.8 3.1 10.9M10.9 10.9 9.8 9.8M4.2 4.2 3.1 3.1" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
    </svg>
  ),
  Search: (p) => (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" {...p}>
      <circle cx="5.2" cy="5.2" r="3.4" stroke="currentColor" strokeWidth="1.2"/>
      <path d="m7.7 7.7 2.6 2.6" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
    </svg>
  ),
  Refresh: (p) => (
    <svg width="13" height="13" viewBox="0 0 14 14" fill="none" {...p}>
      <path d="M11.6 6A4.6 4.6 0 1 0 12 8.4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round"/>
      <path d="M12.4 2.6V6h-3.4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  ),
  Filter: (p) => (
    <svg width="13" height="13" viewBox="0 0 14 14" fill="none" {...p}>
      <path d="M2 3h10l-3.7 4.5v3.7L5.7 12.5V7.5L2 3Z" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round"/>
    </svg>
  ),
  ArrowU: (p) => (<svg width="9" height="9" viewBox="0 0 9 9" fill="none" {...p}><path d="M4.5 7.5V1.5M2 4l2.5-2.5L7 4" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/></svg>),
  ArrowD: (p) => (<svg width="9" height="9" viewBox="0 0 9 9" fill="none" {...p}><path d="M4.5 1.5v6M2 5l2.5 2.5L7 5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/></svg>),
  ChevR: (p) => (<svg width="9" height="9" viewBox="0 0 9 9" fill="none" {...p}><path d="m3.5 1.5 3 3-3 3" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"/></svg>),
  Back: (p) => (<svg width="13" height="13" viewBox="0 0 14 14" fill="none" {...p}><path d="M8 3.5 4.5 7 8 10.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/></svg>),
};

const Avatar = ({ handle, size = 'sm', children }) => (
  <span className={`av av-${handle} ${size === 'lg' ? 'lg' : ''}`}>
    {children || (handle ? handle.slice(0,2).toUpperCase() : '')}
  </span>
);

function scoreClass(s) {
  if (s >= 80) return 'good';
  if (s >= 60) return 'warn';
  return 'bad';
}
const Score = ({ value }) => <span className={`score ${scoreClass(value)}`}>{value}</span>;

const Harness = ({ id, data }) => {
  const h = data.harnesses[id];
  if (!h) return null;
  return (
    <span className="harness">
      <span className="sw" style={{ background: h.tint }}></span>
      {h.label}
    </span>
  );
};

const StatusChip = ({ status }) => (
  <span className={`status-chip ${status}`}>
    <span className="d"></span>
    {status}
  </span>
);

// Sparkline using bars
const Sparkline = ({ values, accent = false }) => (
  <div className={`spark ${accent ? 'accent' : ''}`}>
    {values.map((v, i) => (
      <span key={i} style={{ height: `${Math.max(8, v * 100)}%` }}></span>
    ))}
  </div>
);

const KPI = ({ label, value, unit, delta, deltaDir, spark, sparkAccent }) => (
  <div className="kpi">
    <div className="kpi-label">{label}</div>
    <div className="kpi-value">
      {value}
      {unit ? <span className="unit">{unit}</span> : null}
    </div>
    <div className="kpi-delta">
      <span className={`kpi-delta ${deltaDir || 'flat'}`}>
        {deltaDir === 'up' ? <Icon.ArrowU /> : deltaDir === 'dn' ? <Icon.ArrowD /> : null}
        {delta}
      </span>
    </div>
    {spark ? <Sparkline values={spark} accent={sparkAccent} /> : null}
  </div>
);

Object.assign(window, { Icon, Avatar, Score, Harness, StatusChip, Sparkline, KPI, scoreClass });
