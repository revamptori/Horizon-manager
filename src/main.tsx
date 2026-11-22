import React, { useCallback, useEffect, useMemo, useState } from 'react';
import '@fontsource-variable/inter';
import '@fontsource/dm-mono/400.css';
import '@fontsource/dm-mono/500.css';
import { createRoot } from 'react-dom/client';
import { listen, invoke, isNative, isAndroid, canHostServers, hostLabel, androidRuntimeNotice, initializeHost, persistValue } from './host';
import {
  Activity, Archive, ArrowDownToLine, ArrowLeft, ArrowUpRight, Check, CheckCircle2, ChevronDown,
  ChevronRight, CircleHelp, Copy, Cpu, Download, ExternalLink, Folder, House, Info, Layers3,
  LoaderCircle, Menu, Moon, MoreHorizontal, Network, Package, Pause, Play, Plus, Radio,
  RefreshCw, Search, Server, Settings, ShieldCheck, Sun, Terminal, Trash2, Upload, Wifi, X,
} from 'lucide-react';
import './style.css';

type Software = 'Vanilla' | 'Paper' | 'Purpur' | 'Fabric' | 'Forge' | 'NeoForge' | 'Spigot';
type ServerProfile = {
  id: string; name: string; directory: string; software: Software; version: string;
  port: number; bindAddress: string; memoryMb: number; installed: boolean; createdAt: string;
  backups: BackupRecord[];
};
type BackupRecord = { path: string; sizeBytes: number; createdAt: string };
type NetInterface = { name: string; ipv4: string; private: boolean; selected: boolean };
type NetSnapshot = { interfaces: NetInterface[]; selectedPrivateIpv4: string | null; recommendedPort: number };
type ServerStatus = { running: boolean; pid: number | null; exitCode: number | null };
type ConsoleLine = { serverId: string; stream: string; timestamp: string; line: string };
type Version = { id: string; versionType: string; releaseTime: string };
type ActivityEntry = { id: string; message: string; at: string; kind: 'success' | 'info' | 'warning' };
type Page = 'Overview' | 'Servers' | 'Plugins & Mods' | 'Networking' | 'Backups' | 'Console' | 'Activity & Logs' | 'Settings';

const STORAGE_KEY = 'horizon.server-profiles.v1';
const EVENTS_KEY = 'horizon.activity.v1';
const primaryPages: { label: Page; icon: typeof House }[] = [
  { label: 'Overview', icon: House }, { label: 'Servers', icon: Server },
  { label: 'Plugins & Mods', icon: Package }, { label: 'Networking', icon: Network }, { label: 'Backups', icon: Archive },
];
const managementPages: { label: Page; icon: typeof House }[] = [
  { label: 'Console', icon: Terminal }, { label: 'Activity & Logs', icon: Activity }, { label: 'Settings', icon: Settings },
];
const softwareOptions: { name: Software; description: string; supported: boolean }[] = [
  { name: 'Vanilla', description: 'Official server from Mojang', supported: true },
  { name: 'Paper', description: 'High-performance plugin server', supported: false },
  { name: 'Purpur', description: 'Configurable Paper fork', supported: false },
  { name: 'Fabric', description: 'Lightweight mod loader', supported: false },
  { name: 'Forge', description: 'Established mod platform', supported: false },
  { name: 'NeoForge', description: 'Modern mod platform', supported: false },
  { name: 'Spigot', description: 'Plugin-focused server', supported: false },
];

function readStored<T>(key: string, fallback: T): T {
  try { const raw = localStorage.getItem(key); return raw ? JSON.parse(raw) as T : fallback; }
  catch { return fallback; }
}
function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
function formatTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}
function errorMessage(error: unknown) { return error instanceof Error ? error.message : String(error); }

function App() {
  const [profiles, setProfiles] = useState<ServerProfile[]>(() => readStored(STORAGE_KEY, []));
  const [activity, setActivity] = useState<ActivityEntry[]>(() => readStored(EVENTS_KEY, []));
  const [page, setPage] = useState<Page>('Overview');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showWizard, setShowWizard] = useState(false);
  const [theme, setTheme] = useState<'dark' | 'light'>(() => (localStorage.getItem('horizon.theme') as 'dark' | 'light') || 'dark');
  const [toast, setToast] = useState<{ message: string; tone: 'success' | 'error' | 'info' } | null>(null);
  const [statuses, setStatuses] = useState<Record<string, ServerStatus>>({});
  const [network, setNetwork] = useState<NetSnapshot | null>(null);
  const [networkError, setNetworkError] = useState('');
  const [consoleLines, setConsoleLines] = useState<ConsoleLine[]>([]);
  const [operation, setOperation] = useState<{ label: string; downloaded: number; total: number | null; bytesPerSecond?: number } | null>(null);
  const [mobileMenu, setMobileMenu] = useState(false);
  const selected = profiles.find((profile) => profile.id === selectedId) || null;

  useEffect(() => {
    const warning = (event: Event) => setToast({ message: String((event as CustomEvent).detail), tone: 'error' });
    window.addEventListener('horizon-host-warning', warning);
    return () => window.removeEventListener('horizon-host-warning', warning);
  }, []);

  useEffect(() => { persistValue(STORAGE_KEY, JSON.stringify(profiles)); }, [profiles]);
  useEffect(() => { persistValue(EVENTS_KEY, JSON.stringify(activity.slice(0, 80))); }, [activity]);
  useEffect(() => { persistValue('horizon.theme', theme); document.documentElement.dataset.theme = theme; }, [theme]);
  useEffect(() => {
    if (!toast) return;
    const timeout = window.setTimeout(() => setToast(null), 4200);
    return () => window.clearTimeout(timeout);
  }, [toast]);

  const notify = useCallback((message: string, tone: 'success' | 'error' | 'info' = 'info') => setToast({ message, tone }), []);
  const record = useCallback((message: string, kind: ActivityEntry['kind'] = 'info') => {
    setActivity((current) => [{ id: crypto.randomUUID(), message, at: new Date().toISOString(), kind }, ...current].slice(0, 80));
  }, []);
  const updateProfile = useCallback((id: string, patch: Partial<ServerProfile>) => {
    setProfiles((current) => current.map((profile) => profile.id === id ? { ...profile, ...patch } : profile));
  }, []);

  const refreshNetwork = useCallback(async () => {
    if (!isNative) { setNetworkError('Interface detection is available in the native Go host. This browser preview cannot inspect the machine network.'); return; }
    try { setNetwork(await invoke<NetSnapshot>('network_snapshot')); setNetworkError(''); }
    catch (error) { setNetworkError(errorMessage(error)); }
  }, []);
  useEffect(() => { void refreshNetwork(); }, [refreshNetwork]);

  useEffect(() => {
    if (!isNative) return;
    let unlistenOutput: (() => void) | undefined;
    let unlistenProgress: (() => void) | undefined;
    let unlistenNetwork: (() => void) | undefined;
    let cancelled = false;
    void listen<ConsoleLine>('server-output', (event) => setConsoleLines((lines) => [...lines.slice(-799), event.payload])).then((fn) => { if (cancelled) fn(); else unlistenOutput = fn; });
    void listen<{ operation: string; downloadedBytes: number; totalBytes?: number | null; bytesPerSecond?: number }>('installation-progress', (event) => {
      setOperation({ label: event.payload.operation, downloaded: event.payload.downloadedBytes, total: event.payload.totalBytes ?? null, bytesPerSecond: event.payload.bytesPerSecond });
    }).then((fn) => { if (cancelled) fn(); else unlistenProgress = fn; });
    void listen('network-changed', () => void refreshNetwork()).then((fn) => { if (cancelled) fn(); else unlistenNetwork = fn; });
    return () => { cancelled = true; unlistenOutput?.(); unlistenProgress?.(); unlistenNetwork?.(); };
  }, [refreshNetwork]);

  useEffect(() => {
    if (!isNative || profiles.length === 0) return;
    let cancelled = false;
    const refresh = async () => {
      const results = await Promise.all(profiles.filter((p) => p.installed).map(async (p) => {
        try { return [p.id, await invoke<ServerStatus>('server_status', { serverId: p.id })] as const; }
        catch { return null; }
      }));
      if (!cancelled) setStatuses((old) => Object.assign({}, old, ...results.filter(Boolean).map((r) => ({ [r![0]]: r![1] }))));
    };
    void refresh();
    const timer = window.setInterval(refresh, 3500);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [profiles]);

  const goto = (next: Page) => { setPage(next); setMobileMenu(false); };
  const create = () => { setSelectedId(null); setShowWizard(true); };
  const runServerAction = async (action: 'start' | 'stop' | 'restart', profile: ServerProfile) => {
    if (!canHostServers) { if (isAndroid) { notify(androidRuntimeNotice, 'info'); return; } notify('Server processes are managed by the native Go host. Open the Go host to start or stop this server.', 'info'); return; }
    try {
      if (action === 'stop' || action === 'restart') {
        await invoke<ServerStatus>('stop_server', { serverId: profile.id });
        setStatuses((current) => ({ ...current, [profile.id]: { running: false, pid: null, exitCode: 0 } }));
        record(`${profile.name} stopped`, 'info');
        if (action === 'stop') { notify(`${profile.name} stopped`, 'success'); return; }
      }
      const status = await invoke<ServerStatus>('start_server', { serverId: profile.id, directory: profile.directory, memoryMb: profile.memoryMb, javaPath: null });
      setStatuses((current) => ({ ...current, [profile.id]: status }));
      record(`${profile.name} ${action === 'restart' ? 'restarted' : 'started'}${status.pid ? ` (PID ${status.pid})` : ''}`, 'success');
      notify(`${profile.name} ${action === 'restart' ? 'restarted' : 'is starting'}`, 'success');
    } catch (error) { notify(errorMessage(error), 'error'); }
  };

  const goToServer = (id: string) => { setSelectedId(id); setPage('Servers'); };

  return <div className="app-shell" data-theme={theme}>
    <aside className={`sidebar ${mobileMenu ? 'sidebar-open' : ''}`}>
      <div className="brand"><div className="brand-mark"><Layers3 size={17} strokeWidth={2.1} /></div><span>Horizon</span><span className="brand-product">SERVER MANAGER</span><button className="icon-button sidebar-close" onClick={() => setMobileMenu(false)} aria-label="Close menu"><X size={17} /></button></div>
      <div className="workspace-label">WORKSPACE <button title="Workspace menu" className="plain-icon"><ChevronDown size={13} /></button></div>
      <nav aria-label="Workspace navigation" className="navigation">
        {primaryPages.map(({ label, icon: Icon }) => <NavItem key={label} label={label} icon={<Icon size={17} />} active={page === label && !selectedId} onClick={() => { setSelectedId(null); goto(label); }} />)}
        <div className="nav-divider" />
        <div className="section-label">MANAGEMENT</div>
        {managementPages.map(({ label, icon: Icon }) => <NavItem key={label} label={label} icon={<Icon size={17} />} active={page === label} onClick={() => { if (label === 'Console' && !selectedId && profiles[0]) setSelectedId(profiles[0].id); goto(label); }} />)}
      </nav>
      <div className="sidebar-bottom">
        <button className="documentation-link" onClick={() => notify('Documentation is being prepared alongside the first desktop release.', 'info')}><CircleHelp size={15} /> Documentation <ArrowUpRight size={13} className="documentation-arrow" /></button>
        <div className="device-card"><div className="device-status-dot" /><div className="device-text"><strong>Local device</strong><span>{isNative ? hostLabel : 'Web preview · control unavailable'}</span></div><MoreHorizontal size={16} className="device-more" /></div>
        <div className="sidebar-version">Horizon Server Manager <span>v0.2.0 · Go preview</span></div>
      </div>
    </aside>
    {mobileMenu && <button className="mobile-scrim" onClick={() => setMobileMenu(false)} aria-label="Close navigation" />}
    <main className="main-column">
      <header className="topbar">
        <button className="icon-button mobile-menu-button" onClick={() => setMobileMenu(true)} aria-label="Open navigation"><Menu size={19} /></button>
        <div className="breadcrumb"><span>{page === 'Overview' ? 'Workspace' : 'Workspace'}</span><ChevronRight size={13} /><strong>{selected && page === 'Servers' ? selected.name : page}</strong></div>
        <div className="topbar-actions"><span className={`host-indicator ${isNative ? 'connected' : ''}`}><i />{isNative ? 'This device' : 'Preview mode'}</span><button className="icon-button theme-button" onClick={() => setTheme((v) => v === 'dark' ? 'light' : 'dark')} aria-label={`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`}>{theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}</button><button className="icon-button top-settings" onClick={() => { setSelectedId(null); goto('Settings'); }} aria-label="Settings"><Settings size={16} /></button></div>
      </header>
      <div className="page-scroll">
        {page === 'Overview' && <Overview profiles={profiles} statuses={statuses} network={network} onCreate={create} onOpen={goToServer} onNavigate={goto} activity={activity} />}
        {page === 'Servers' && (selected ? <ServerDetail profile={selected} running={!!statuses[selected.id]?.running} status={statuses[selected.id]} onBack={() => setSelectedId(null)} onAction={runServerAction} onNavigate={goto} onUpdate={updateProfile} notify={notify} onCreateBackup={async (p) => {
          if (!isNative) { notify('Backups are created by the native Go host. This preview cannot access server files.'); return; }
          try {
            const backup = await invoke<BackupRecord>('create_backup', { serverId: p.id, directory: p.directory });
            updateProfile(p.id, { backups: [backup, ...p.backups] }); record(`Backup created for ${p.name} · ${formatBytes(backup.sizeBytes)}`, 'success'); notify('Backup created', 'success');
          } catch (error) { notify(errorMessage(error), 'error'); }
        }} /> : <ServersPage profiles={profiles} statuses={statuses} onCreate={create} onOpen={goToServer} onDelete={(p) => {
          if (statuses[p.id]?.running) { notify('Stop this server before removing its profile.', 'error'); return; }
          if (!window.confirm(`Remove “${p.name}” from Horizon? This only removes its local profile. It does not delete server files.`)) return;
          setProfiles((rows) => rows.filter((row) => row.id !== p.id)); record(`${p.name} removed from Horizon`, 'warning'); notify('Server profile removed');
        }} />)}
        {page === 'Plugins & Mods' && <PackagesPage />}
        {page === 'Networking' && <NetworkingPage profile={selected || profiles[0] || null} profiles={profiles} running={!!statuses[(selected || profiles[0])?.id || '']?.running} onChooseServer={setSelectedId} network={network} networkError={networkError} onRefresh={refreshNetwork} onSelectServer={updateProfile} notify={notify} record={record} />}
        {page === 'Backups' && <BackupsPage profiles={profiles} statuses={statuses} isNative={isNative} notify={notify} updateProfile={updateProfile} record={record} />}
        {page === 'Console' && <ConsolePage profiles={profiles} statuses={statuses} lines={consoleLines} isNative={isNative} notify={notify} onClear={(id) => setConsoleLines((old) => old.filter((line) => line.serverId !== id))} />}
        {page === 'Activity & Logs' && <ActivityPage activity={activity} />}
        {page === 'Settings' && <SettingsPage theme={theme} setTheme={setTheme} isNative={isNative} onRefresh={refreshNetwork} />}
      </div>
    </main>
    <MobileNav page={page} setPage={(next) => { setSelectedId(null); goto(next); }} />
    {showWizard && <CreateWizard onClose={() => { setShowWizard(false); setOperation(null); }} onDone={(profile, message) => {
      setProfiles((current) => [profile, ...current]); setSelectedId(profile.id); setPage('Servers'); setShowWizard(false); setOperation(null); record(message, profile.installed ? 'success' : 'info'); notify(profile.installed ? 'Server installed and ready to start' : 'Server profile created', profile.installed ? 'success' : 'info');
    }} onError={(message) => { setOperation(null); notify(message, 'error'); }} network={network} isNative={isNative} operation={operation} setOperation={setOperation} />}
    {toast && <div className={`toast toast-${toast.tone}`} role="status"><span className="toast-symbol">{toast.tone === 'success' ? <CheckCircle2 size={17} /> : toast.tone === 'error' ? <Info size={17} /> : <Info size={17} />}</span><span>{toast.message}</span><button onClick={() => setToast(null)} aria-label="Dismiss notification"><X size={14} /></button></div>}
  </div>;
}

function NavItem({ label, icon, active, onClick }: { label: string; icon: React.ReactNode; active: boolean; onClick: () => void }) {
  return <button className={`nav-item ${active ? 'active' : ''}`} onClick={onClick}>{icon}<span>{label}</span>{label === 'Servers' && <span className="nav-count">{(readStored<ServerProfile[]>(STORAGE_KEY, [])).length || ''}</span>}</button>;
}

function PageHeading({ title, description, action }: { title: string; description: string; action?: React.ReactNode }) {
  return <div className="page-heading"><div><h1>{title}</h1><p>{description}</p></div>{action && <div className="heading-actions">{action}</div>}</div>;
}
function PrimaryButton({ children, onClick, icon, disabled, type = 'button' }: { children: React.ReactNode; onClick?: () => void; icon?: React.ReactNode; disabled?: boolean; type?: 'button' | 'submit' }) {
  return <button type={type} className="button button-primary" onClick={onClick} disabled={disabled}>{icon}{children}</button>;
}
function QuietButton({ children, onClick, icon, disabled, className = '' }: { children: React.ReactNode; onClick?: () => void; icon?: React.ReactNode; disabled?: boolean; className?: string }) {
  return <button className={`button button-quiet ${className}`} onClick={onClick} disabled={disabled}>{icon}{children}</button>;
}
function StatusBadge({ running }: { running: boolean }) {
  return <span className={`status-badge ${running ? 'status-running' : 'status-stopped'}`}><i />{running ? 'Running' : 'Stopped'}</span>;
}
function SoftwareMark({ software }: { software: Software }) { return <span className={`software-mark software-${software.toLowerCase()}`}>{software === 'Vanilla' ? <Layers3 size={14} /> : <Package size={14} />}</span>; }

function Overview({ profiles, statuses, network, onCreate, onOpen, onNavigate, activity }: { profiles: ServerProfile[]; statuses: Record<string, ServerStatus>; network: NetSnapshot | null; onCreate: () => void; onOpen: (id: string) => void; onNavigate: (page: Page) => void; activity: ActivityEntry[] }) {
  const runningCount = profiles.filter((p) => statuses[p.id]?.running).length;
  return <>
    <PageHeading title="Overview" description="Your local Minecraft hosting workspace." action={<PrimaryButton icon={<Plus size={16} />} onClick={onCreate}>Create server</PrimaryButton>} />
    {isAndroid && <div className="notice-banner"><Info size={17} /><div><strong>Android runtime not included</strong><span>{androidRuntimeNotice}</span></div></div>}
    <div className="overview-summary-row">
      <section className="summary-block"><span className="eyebrow">HOST STATUS</span><div className="summary-main"><span className={`summary-dot ${profiles.length ? 'summary-dot-live' : ''}`} /><strong>{isAndroid ? 'Android file manager ready' : isNative ? 'Local host ready' : 'Native host not connected'}</strong></div><span className="summary-caption">{isAndroid ? 'JVM hosting unavailable · see runtime notice' : isNative ? 'Server processes run on this device' : 'Open the installed app to manage server processes'}</span></section>
      <section className="summary-block"><span className="eyebrow">SERVERS</span><div className="summary-main"><strong>{profiles.length}</strong><span className="summary-caption inline">configured</span><span className="summary-separator">·</span><strong className="running-text">{runningCount}</strong><span className="summary-caption inline">running</span></div><span className="summary-caption">Local profiles on this device</span></section>
      <section className="summary-block"><span className="eyebrow">LAN ADDRESS</span><div className="summary-main"><code className="summary-address">{network?.selectedPrivateIpv4 || 'Not detected'}</code></div><span className="summary-caption">{network ? `Suggested port ${network.recommendedPort}` : 'Available in the installed app'}</span></section>
    </div>
    <div className="section-heading"><div><h2>Servers</h2><span>Local instances configured for this device</span></div><button className="text-button" onClick={() => onNavigate('Servers')}>View all <ArrowUpRight size={14} /></button></div>
    {profiles.length ? <div className="server-table-wrap"><table className="server-table"><thead><tr><th>SERVER</th><th>SOFTWARE</th><th>STATUS</th><th>PLAYERS</th><th>MEMORY</th><th>PORT</th><th></th></tr></thead><tbody>{profiles.slice(0, 6).map((profile) => <tr key={profile.id} onClick={() => onOpen(profile.id)}><td><div className="server-name-cell"><SoftwareMark software={profile.software} /><span><strong>{profile.name}</strong><small>{profile.version} · Minecraft</small></span></div></td><td>{profile.software}</td><td><StatusBadge running={!!statuses[profile.id]?.running} /></td><td><span className="table-muted">{statuses[profile.id]?.running ? '—' : '—'}</span></td><td><span className="mono table-muted">{profile.memoryMb >= 1024 ? `${(profile.memoryMb / 1024).toFixed(profile.memoryMb % 1024 ? 1 : 0)} GB` : `${profile.memoryMb} MB`}</span></td><td><code className="mono">{profile.port}</code></td><td><ChevronRight size={16} className="row-chevron" /></td></tr>)}</tbody></table></div> : <div className="empty-panel"><div className="empty-icon"><Server size={22} /></div><h2>No servers created yet</h2><p>Create a Minecraft server to start hosting locally.</p><PrimaryButton icon={<Plus size={16} />} onClick={onCreate}>Create your first server</PrimaryButton></div>}
    <div className="overview-lower-grid">
      <section className="panel resource-panel"><div className="panel-title-row"><div><h3>Resource usage</h3><p>Process-level measurements</p></div><Cpu size={17} /></div><div className="unavailable-line"><div className="unavailable-indicator"><Activity size={15} /></div><div><strong>Metrics are not available yet</strong><span>CPU and memory sampling will appear here when process monitoring is enabled.</span></div></div></section>
      <section className="panel tunnel-panel"><div className="panel-title-row"><div><h3>Remote connectivity</h3><p>Cloudflare Tunnel</p></div><Radio size={17} /></div><div className="tunnel-status-row"><span className="tunnel-status-dot" /><div><strong>Not configured</strong><span>Remote TCP access needs a supported client or proxy setup.</span></div></div><button className="text-button" onClick={() => onNavigate('Networking')}>Review networking <ArrowUpRight size={14} /></button></section>
    </div>
    <section className="activity-section"><div className="section-heading compact"><div><h2>Recent activity</h2><span>Local operations recorded by this app</span></div><button className="text-button" onClick={() => onNavigate('Activity & Logs')}>View log <ArrowUpRight size={14} /></button></div>{activity.length ? <div className="activity-list">{activity.slice(0, 3).map((item) => <div className="activity-item" key={item.id}><span className={`activity-mark activity-${item.kind}`}><Check size={13} /></span><span className="activity-message">{item.message}</span><time>{formatTime(item.at)}</time></div>)}</div> : <div className="activity-empty">Operations will appear here as you configure and manage servers.</div>}</section>
  </>;
}

function ServersPage({ profiles, statuses, onCreate, onOpen, onDelete }: { profiles: ServerProfile[]; statuses: Record<string, ServerStatus>; onCreate: () => void; onOpen: (id: string) => void; onDelete: (p: ServerProfile) => void }) {
  return <><PageHeading title="Servers" description="Manage server instances hosted on this device." action={<PrimaryButton icon={<Plus size={16} />} onClick={onCreate}>Create server</PrimaryButton>} />
    {!profiles.length ? <div className="empty-panel list-empty"><div className="empty-icon"><Server size={22} /></div><h2>No servers yet</h2><p>Each server gets its own directory, runtime settings, and network binding.</p><PrimaryButton icon={<Plus size={16} />} onClick={onCreate}>Create server</PrimaryButton></div> : <><div className="table-toolbar"><span>{profiles.length} {profiles.length === 1 ? 'server' : 'servers'}</span><span className="table-help">Select a server to manage its process and configuration.</span></div><div className="server-table-wrap"><table className="server-table server-table-full"><thead><tr><th>SERVER</th><th>SOFTWARE</th><th>STATUS</th><th>PLAYERS</th><th>MEMORY</th><th>PORT</th><th>ACTIONS</th></tr></thead><tbody>{profiles.map((profile) => <tr key={profile.id} onClick={() => onOpen(profile.id)}><td><div className="server-name-cell"><SoftwareMark software={profile.software} /><span><strong>{profile.name}</strong><small>{profile.version} · {profile.installed ? 'Installed' : 'Profile only'}</small></span></div></td><td>{profile.software}</td><td><StatusBadge running={!!statuses[profile.id]?.running} /></td><td><span className="table-muted">—</span></td><td><span className="mono">{profile.memoryMb} MB</span></td><td><code className="mono">{profile.port}</code></td><td><div className="table-actions"><button className="icon-button row-action" aria-label={`Open ${profile.name}`} onClick={(event) => { event.stopPropagation(); onOpen(profile.id); }}><ChevronRight size={16} /></button><button className="icon-button row-action remove-profile" aria-label={`Remove ${profile.name} profile`} title="Remove profile (files are kept)" onClick={(event) => { event.stopPropagation(); onDelete(profile); }}><Trash2 size={14} /></button></div></td></tr>)}</tbody></table></div><div className="local-data-note"><Info size={15} /><span>Player count and per-process resource usage are not yet connected to a Minecraft status sampler. No values are estimated.</span></div></>}
  </>;
}

function ServerDetail({ profile, running, status, onBack, onAction, onNavigate, onUpdate, notify, onCreateBackup }: { profile: ServerProfile; running: boolean; status?: ServerStatus; onBack: () => void; onAction: (action: 'start' | 'stop' | 'restart', profile: ServerProfile) => void; onNavigate: (page: Page) => void; onUpdate: (id: string, patch: Partial<ServerProfile>) => void; notify: (message: string, tone?: 'success' | 'error' | 'info') => void; onCreateBackup: (p: ServerProfile) => void }) {
  const [tab, setTab] = useState<'Summary' | 'Configuration'>('Summary');
  const statusText = status?.exitCode !== null && status?.exitCode !== undefined ? `Exited with code ${status.exitCode}` : profile.installed ? 'Stopped' : 'Not installed';
  return <>
    <button className="back-link" onClick={onBack}><ArrowLeft size={15} /> All servers</button>
    <div className="server-detail-heading"><div className="server-detail-identity"><SoftwareMark software={profile.software} /><div><div className="detail-title-row"><h1>{profile.name}</h1><StatusBadge running={running} /></div><p>{profile.software} <span>·</span> Minecraft {profile.version} <span>·</span> {running && status?.pid ? `PID ${status.pid}` : statusText}</p></div></div><div className="detail-actions">{running ? <><QuietButton icon={<RefreshCw size={15} />} onClick={() => onAction('restart', profile)}>Restart</QuietButton><button className="button button-danger" onClick={() => onAction('stop', profile)}><Pause size={15} /> Stop</button></> : <><QuietButton icon={<Archive size={15} />} disabled={!profile.installed} onClick={() => onCreateBackup(profile)}>Backup</QuietButton><PrimaryButton icon={<Play size={15} />} disabled={!profile.installed || !canHostServers} onClick={() => onAction('start', profile)}>Start server</PrimaryButton></>}</div></div>
    {isAndroid && <div className="notice-banner"><Info size={17} /><div><strong>Hosting unavailable on Android</strong><span>{androidRuntimeNotice}</span></div></div>}
    <div className="detail-tabs">{(['Summary', 'Configuration'] as const).map((name) => <button key={name} className={tab === name ? 'selected' : ''} onClick={() => setTab(name)}>{name}</button>)}</div>
    {tab === 'Summary' ? <div className="detail-grid">
      <section className="panel detail-console-panel"><div className="panel-title-row"><div><h3>Console</h3><p>Live process output</p></div><button className="text-button" onClick={() => onNavigate('Console')}>Open console <ArrowUpRight size={14} /></button></div><div className="console-preview">{!profile.installed ? <div className="console-placeholder"><span className="console-prompt">›</span> Install the server to see process output.</div> : !running ? <div className="console-placeholder"><span className="console-prompt">›</span> Server is stopped. Start it to stream output.</div> : <div className="console-placeholder"><span className="console-prompt">›</span> Process is running. Open the console for live output.</div>}</div><button className="console-open-link" onClick={() => onNavigate('Console')}>View terminal <ArrowUpRight size={13} /></button></section>
      <section className="panel connection-panel"><div className="panel-title-row"><div><h3>Connection</h3><p>Local network access</p></div><Wifi size={17} /></div><div className="connection-line"><span>Local address</span><code>{profile.bindAddress === '0.0.0.0' ? 'All IPv4 interfaces' : profile.bindAddress}:{profile.port}</code></div><div className="connection-footnote">{profile.bindAddress === '0.0.0.0' ? 'Listening on all IPv4 interfaces. Players connect using this device’s private IP, not 0.0.0.0.' : 'Bound to a private interface. Other devices on your LAN may connect if firewall rules allow it.'}</div><button className="text-button" onClick={() => onNavigate('Networking')}>Network settings <ArrowUpRight size={14} /></button></section>
      <section className="panel detail-info-panel"><div className="panel-title-row"><div><h3>Server configuration</h3><p>Instance settings</p></div><Settings size={17} /></div><InfoRow label="Software" value={`${profile.software} · ${profile.version}`} /><InfoRow label="Memory allocation" value={`${profile.memoryMb} MB`} mono /><InfoRow label="Port" value={String(profile.port)} mono /><InfoRow label="Server directory" value={profile.directory} mono /></section>
      <section className="panel detail-info-panel"><div className="panel-title-row"><div><h3>Backups</h3><p>{profile.backups.length ? `${profile.backups.length} saved` : 'No backups yet'}</p></div><Archive size={17} /></div>{profile.backups.length ? <div className="backup-mini-list">{profile.backups.slice(0, 2).map((backup) => <div key={backup.path}><span><strong>{formatTime(backup.createdAt)}</strong><small>{formatBytes(backup.sizeBytes)}</small></span><CheckCircle2 size={15} /></div>)}</div> : <div className="mini-empty">Create a snapshot before a major change or upgrade.</div>}<QuietButton className="full-width-button" icon={<Archive size={15} />} disabled={!profile.installed || running} onClick={() => onCreateBackup(profile)}>Create backup</QuietButton></section>
    </div> : <div className="configuration-content"><section className="panel config-panel"><div className="panel-title-row"><div><h3>Instance settings</h3><p>Configured during server creation</p></div><button className="button button-quiet" onClick={() => { onUpdate(profile.id, { memoryMb: Math.min(32768, profile.memoryMb + 512) }); notify('Memory increased by 512 MB.'); }}>Increase memory</button></div><InfoRow label="Server name" value={profile.name} /><InfoRow label="Minecraft version" value={profile.version} /><InfoRow label="Server software" value={profile.software} /><InfoRow label="Memory allocation" value={`${profile.memoryMb} MB`} /><InfoRow label="Network binding" value={`${profile.bindAddress}:${profile.port}`} mono /></section><section className="panel config-panel"><div className="panel-title-row"><div><h3>Files and data</h3><p>Horizon does not delete instance files when removing a profile.</p></div><Folder size={17} /></div><InfoRow label="Server directory" value={profile.directory} mono /><div className="config-warning"><ShieldCheck size={16} /><span>Existing worlds are never overwritten during installation. Backups restore into a new folder rather than replacing an active world.</span></div></section></div>}
  </>;
}
function InfoRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div className="info-row"><span>{label}</span><code className={mono ? 'mono' : ''} title={value}>{value}</code></div>; }

function CreateWizard({ onClose, onDone, onError, network, isNative, operation, setOperation }: { onClose: () => void; onDone: (profile: ServerProfile, message: string) => void; onError: (message: string) => void; network: NetSnapshot | null; isNative: boolean; operation: { label: string; downloaded: number; total: number | null; bytesPerSecond?: number } | null; setOperation: (operation: { label: string; downloaded: number; total: number | null; bytesPerSecond?: number } | null) => void }) {
  const [step, setStep] = useState(0);
  const [name, setName] = useState('');
  const [directory, setDirectory] = useState('');
  const [software, setSoftware] = useState<Software>('Vanilla');
  const [version, setVersion] = useState('');
  const [versions, setVersions] = useState<Version[]>([]);
  const [versionLoading, setVersionLoading] = useState(true);
  const [versionError, setVersionError] = useState('');
  const [memoryMb, setMemoryMb] = useState(() => Number(localStorage.getItem('horizon.default-memory') || '2048'));
  const [bindAddress, setBindAddress] = useState(network?.selectedPrivateIpv4 || '');
  const [port, setPort] = useState(network?.recommendedPort || 25565);
  const [portAvailable, setPortAvailable] = useState<boolean | null>(null);
  const [acceptEula, setAcceptEula] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [directoryTouched, setDirectoryTouched] = useState(false);
  const [pathLoading, setPathLoading] = useState(false);

  useEffect(() => { if (network?.selectedPrivateIpv4 && !bindAddress) setBindAddress(network.selectedPrivateIpv4); if (network?.recommendedPort && port === 25565) setPort(network.recommendedPort); }, [network]);
  useEffect(() => {
    if (!isNative || !bindAddress || port < 1024 || port > 65535) { setPortAvailable(null); return; }
    let active = true;
    setPortAvailable(null);
    void invoke<boolean>('check_port_available', { address: bindAddress, port }).then((available) => { if (active) setPortAvailable(available); }).catch(() => { if (active) setPortAvailable(false); });
    return () => { active = false; };
  }, [isNative, bindAddress, port]);
  useEffect(() => {
    let active = true;
    const load = async () => {
      setVersionLoading(true); setVersionError('');
      try {
        const result = isNative ? await invoke<Version[]>('list_minecraft_versions') : await fetch('https://piston-meta.mojang.com/mc/game/version_manifest_v2.json').then((r) => { if (!r.ok) throw new Error(`Mojang returned HTTP ${r.status}`); return r.json(); }).then((data: { versions: Array<{ id: string; type: string; releaseTime: string }> }) => data.versions.filter((v) => v.type === 'release').slice(0, 30).map((v) => ({ id: v.id, versionType: v.type, releaseTime: v.releaseTime })));
        if (active) { setVersions(result); setVersion(result[0]?.id || ''); }
      } catch (error) { if (active) setVersionError(errorMessage(error)); }
      finally { if (active) setVersionLoading(false); }
    };
    void load(); return () => { active = false; };
  }, [isNative]);
  useEffect(() => {
    if (!name.trim() || directoryTouched) return;
    if (!isNative) { setDirectory(`Horizon Servers/${name.trim().replace(/[^\w-]+/g, '-')}`); return; }
    let current = true;
    setPathLoading(true);
    void invoke<string>('default_server_directory', { name }).then((path) => { if (current && !directoryTouched) setDirectory(path); }).catch(() => undefined).finally(() => { if (current) setPathLoading(false); });
    return () => { current = false; };
  }, [name, isNative, directoryTouched]);

  const steps = ['Identity', 'Software', 'Runtime', 'Review'];
  const canNext = step === 0 ? !!name.trim() && !!directory.trim() : step === 1 ? software === 'Vanilla' && !!version : step === 2 ? memoryMb >= 512 && !!bindAddress && port >= 1024 && port <= 65535 && (!isNative || portAvailable === true) : (!isNative || acceptEula);
  const nextStep = () => setStep((value) => Math.min(3, value + 1));
  const installOrSave = async () => {
    if (!name.trim() || !directory.trim() || !version) return;
    const profile: ServerProfile = { id: crypto.randomUUID().replace(/-/g, '').slice(0, 24), name: name.trim(), directory: directory.trim(), software, version, port, bindAddress, memoryMb, installed: false, createdAt: new Date().toISOString(), backups: [] };
    if (!isNative) { onDone(profile, `Server profile created for ${profile.name}; installation is pending the native Go host`); return; }
    setInstalling(true); setOperation({ label: 'Preparing server directory', downloaded: 0, total: null });
    try {
      await invoke<string>('prepare_server_directory', { directory: profile.directory });
      setOperation({ label: 'Connecting to Mojang metadata', downloaded: 0, total: null });
      const installed = await invoke<{ jarPath: string; version: string; bytesDownloaded: number; sha1Verified: boolean }>('install_vanilla_server', { serverId: profile.id, name: profile.name, memoryMb: profile.memoryMb, version: profile.version, directory: profile.directory, bindAddress: profile.bindAddress, port: profile.port, acceptEula });
      if (!installed.sha1Verified) throw new Error('Server download could not be verified.');
      profile.installed = true;
      setOperation(null);
      onDone(profile, `${profile.name} installed · ${formatBytes(installed.bytesDownloaded)} verified`);
    } catch (error) { setInstalling(false); setOperation(null); onError(errorMessage(error)); }
  };

  return <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !installing) onClose(); }}><section className="wizard-modal" role="dialog" aria-modal="true" aria-labelledby="wizard-title"><header className="wizard-header"><div><span className="eyebrow">NEW INSTANCE</span><h2 id="wizard-title">Create a server</h2></div><button className="icon-button" onClick={onClose} disabled={installing} aria-label="Close create server"><X size={18} /></button></header><div className="wizard-stepper">{steps.map((label, index) => <React.Fragment key={label}><div className={`step-item ${index === step ? 'step-current' : ''} ${index < step ? 'step-complete' : ''}`}><span className="step-number">{index < step ? <Check size={13} /> : index + 1}</span><span>{label}</span></div>{index < steps.length - 1 && <span className={`step-connector ${index < step ? 'step-connector-complete' : ''}`} />}</React.Fragment>)}</div><div className="wizard-content">
      {step === 0 && <div className="wizard-fields"><div className="wizard-intro"><h3>Give your server an identity</h3><p>Each instance uses its own directory. Horizon never installs over an existing world.</p></div><label className="field-label">Server name<input autoFocus value={name} maxLength={48} placeholder="e.g. Survival" onChange={(event) => setName(event.target.value)} /><span className="field-help">A name for your local server list.</span></label><label className="field-label">Server directory<div className="input-with-icon"><Folder size={16} /><input value={directory} placeholder={pathLoading ? 'Choosing a folder…' : 'Choose a server folder'} onChange={(event) => { setDirectoryTouched(true); setDirectory(event.target.value); }} /><button className="folder-choose" disabled title="Choose folders from the native installed app in a future update"><MoreHorizontal size={16} /></button></div><span className="field-help">{isNative ? 'The folder must be empty. Pick a new location for an existing server.' : 'Browser preview stores only a local profile. Native desktop access is required to use this path.'}</span></label></div>}
      {step === 1 && <div className="wizard-fields"><div className="wizard-intro"><h3>Choose server software</h3><p>Only implemented integrations are available. Downloaded files are checked against Mojang metadata.</p></div><div className="software-grid">{softwareOptions.map((option) => <button key={option.name} className={`software-option ${software === option.name ? 'software-option-selected' : ''} ${!option.supported ? 'software-option-disabled' : ''}`} onClick={() => option.supported && setSoftware(option.name)} disabled={!option.supported}><SoftwareMark software={option.name} /><span className="software-option-copy"><strong>{option.name}</strong><small>{option.description}</small></span>{option.supported ? software === option.name ? <CheckCircle2 size={16} /> : <span className="radio-outline" /> : <span className="coming-label">Planned</span>}</button>)}</div><label className="field-label">Minecraft version<select value={version} onChange={(event) => setVersion(event.target.value)} disabled={versionLoading || !versions.length}><option value="">{versionLoading ? 'Fetching official releases…' : versions.length ? 'Select a release' : 'Versions unavailable'}</option>{versions.map((v) => <option key={v.id} value={v.id}>{v.id}</option>)}</select><span className="field-help">{versionError ? `Could not load Mojang's official release list: ${versionError}` : 'Release versions are read from Mojang’s official version manifest.'}</span></label></div>}
      {step === 2 && <div className="wizard-fields"><div className="wizard-intro"><h3>Runtime and network</h3><p>Choose memory and the interface the server will listen on.</p></div><label className="field-label">Memory allocation<div className="range-row"><input type="range" min="512" max="32768" step="256" value={memoryMb} onChange={(event) => setMemoryMb(Number(event.target.value))} /><code className="range-value">{memoryMb >= 1024 ? `${(memoryMb / 1024).toFixed(memoryMb % 1024 ? 1 : 0)} GB` : `${memoryMb} MB`}</code></div><span className="field-help">This is the JVM maximum heap, not a limit on total process memory. Ensure the device has headroom for its operating system.</span></label><label className="field-label">Private interface<select value={bindAddress} onChange={(event) => setBindAddress(event.target.value)}><option value="">{isNative ? 'No private IPv4 detected' : 'Desktop detection required'}</option>{network?.interfaces.filter((iface) => iface.private).map((iface) => <option key={`${iface.name}-${iface.ipv4}`} value={iface.ipv4}>{iface.ipv4} · {iface.name}{iface.selected ? ' · selected route' : ''}</option>)}<option value="0.0.0.0">0.0.0.0 · all IPv4 interfaces</option></select><span className="field-help">Private interface is preferred. 0.0.0.0 listens on every IPv4 interface; it is not the address players use to connect.</span></label><div className="field-label">Server port<div className="port-input-row"><input type="number" min={1024} max={65535} value={port} onChange={(event) => setPort(Number(event.target.value))} /><span className={`field-help ${isNative && portAvailable === false ? 'port-unavailable' : ''}`}>{isNative ? port < 1024 || port > 65535 ? 'Choose a port from 1024 to 65535.' : portAvailable === null ? 'Checking port availability…' : portAvailable ? `Port is available on ${bindAddress}.` : 'This port is in use or could not be checked. Choose another.' : 'The native Go host will check this port before installation.'}</span></div></div><div className="runtime-note"><Info size={16} /><span>{isAndroid ? androidRuntimeNotice : 'Horizon uses desktop Java from PATH. Install the JVM required by your Minecraft release (recent releases commonly require Java 21). Automatic runtime installation is not implemented.'}</span></div></div>}
      {step === 3 && <div className="wizard-fields"><div className="wizard-intro"><h3>Review and install</h3><p>Review the server settings before downloading official server files.</p></div><div className="review-list"><InfoRow label="Server name" value={name} /><InfoRow label="Software" value={`Vanilla · ${version}`} /><InfoRow label="Memory" value={`${memoryMb} MB`} /><InfoRow label="Binding" value={`${bindAddress}:${port}`} mono /><InfoRow label="Directory" value={directory} mono /></div>{isNative && <label className="eula-check"><input type="checkbox" checked={acceptEula} onChange={(event) => setAcceptEula(event.target.checked)} /><span>I have read and agree to the <a href="https://www.minecraft.net/eula" target="_blank" rel="noreferrer">Minecraft EULA <ExternalLink size={12} /></a>. Horizon will save this acceptance in the server folder.</span></label>}{!isNative && <div className="browser-limitation"><Info size={16} /><span>In browser preview you can save this server profile only. Server download, filesystem changes, and process controls require the native installed app.</span></div>}{operation && <div className="install-progress"><div className="progress-title"><LoaderCircle size={15} className="spin" />{operation.label}<span>{`${formatBytes(operation.downloaded)}${operation.total ? ` / ${formatBytes(operation.total)}` : ''} · ${formatBytes(operation.bytesPerSecond || 0)}/s`}</span></div><div className={`progress-track ${operation.total ? '' : 'progress-indeterminate'}`}><i style={{ width: operation.total ? `${Math.min(100, operation.downloaded / operation.total * 100)}%` : '35%' }} /></div>{operation.total && <small>Download progress from the active transfer · checksum verification follows.</small>}</div>}</div>}
    </div><footer className="wizard-footer"><div>{step > 0 && !installing && <QuietButton icon={<ArrowLeft size={15} />} onClick={() => setStep((v) => Math.max(0, v - 1))}>Back</QuietButton>}</div><div><QuietButton onClick={onClose} disabled={installing}>Cancel</QuietButton>{step < 3 ? <PrimaryButton onClick={nextStep} disabled={!canNext}>Continue <ChevronRight size={15} /></PrimaryButton> : <PrimaryButton onClick={installOrSave} disabled={!canNext || installing} icon={installing ? <LoaderCircle size={15} className="spin" /> : isNative ? <Download size={15} /> : <Check size={15} />}>{installing ? 'Installing…' : isNative ? 'Accept EULA & install' : 'Save profile'}</PrimaryButton>}</div></footer></section></div>;
}

function PackagesPage() {
  const [tab, setTab] = useState<'Discover' | 'Installed'>('Discover');
  return <><PageHeading title="Plugins & Mods" description="Package catalogs and compatibility checks for your server software." /><div className="notice-banner"><Info size={17} /><div><strong>Package sources are not connected yet</strong><span>Modrinth, Hangar, and CurseForge integrations are planned. No catalog results or install actions are simulated.</span></div></div><div className="tab-row">{(['Discover', 'Installed'] as const).map((name) => <button key={name} className={tab === name ? 'tab-selected' : ''} onClick={() => setTab(name)}>{name}</button>)}</div><div className="catalog-toolbar"><div className="search-box"><Search size={16} /><input disabled placeholder="Search projects" /></div><button className="button button-quiet" disabled><span>Filters</span><ChevronDown size={14} /></button></div><div className="empty-panel catalog-empty"><div className="empty-icon"><Package size={21} /></div><h2>{tab === 'Discover' ? 'Catalog unavailable' : 'No installed packages tracked'}</h2><p>{tab === 'Discover' ? 'Connect a supported project catalog to discover verified plugins and mods.' : 'Installed package scanning will be added with the catalog integration.'}</p><span className="text-note">Vanilla servers do not support third-party plugins or mods.</span></div></>;
}

function NetworkingPage({ profile, profiles, running, onChooseServer, network, networkError, onRefresh, onSelectServer, notify, record }: { profile: ServerProfile | null; profiles: ServerProfile[]; running: boolean; onChooseServer: (id: string) => void; network: NetSnapshot | null; networkError: string; onRefresh: () => Promise<void>; onSelectServer: (id: string, patch: Partial<ServerProfile>) => void; notify: (message: string, tone?: 'success' | 'error' | 'info') => void; record: (message: string, kind?: ActivityEntry['kind']) => void }) {
  const [busy, setBusy] = useState(false);
  const [publicMode, setPublicMode] = useState(profile?.bindAddress === '0.0.0.0');
  const [selectedPort, setSelectedPort] = useState(profile?.port || 25565);
  useEffect(() => { setPublicMode(profile?.bindAddress === '0.0.0.0'); setSelectedPort(profile?.port || 25565); }, [profile?.id, profile?.bindAddress, profile?.port]);
  const localIp = network?.selectedPrivateIpv4 || network?.interfaces.find((i) => i.private)?.ipv4;
  const apply = async () => {
    if (!profile) { notify('Create or select a server before changing its binding.'); return; }
    if (!isNative) { notify('Network binding changes require the native Go host.'); return; }
    const address = publicMode ? '0.0.0.0' : (localIp || '');
    if (!address) { notify('No private IPv4 address is currently available. Refresh interface detection.', 'error'); return; }
    if (running) { notify('Stop the server before changing its binding. The new address applies on the next start.', 'error'); return; }
    setBusy(true);
    try {
      await invoke('set_server_binding', { serverId: profile.id, directory: profile.directory, address, port: selectedPort });
      onSelectServer(profile.id, { bindAddress: address, port: selectedPort });
      record(`${profile.name} binding changed to ${address}:${selectedPort}`, 'success');
      notify(`Binding updated to ${address}:${selectedPort}. Changes apply on next start.`, 'success');
    } catch (error) { notify(errorMessage(error), 'error'); }
    finally { setBusy(false); }
  };
  const copyAddress = async () => {
    if (!localIp || !profile) return;
    await navigator.clipboard?.writeText(`${localIp}:${profile.port}`);
    notify('LAN address copied', 'success');
  };
  return <><PageHeading title="Networking" description="Local binding, LAN access, and remote connectivity." action={<QuietButton icon={<RefreshCw size={15} />} onClick={() => void onRefresh()}>Refresh detection</QuietButton>} />
    <div className="network-layout"><div className="network-main">
      <section className="panel network-card"><div className="network-card-header"><div className="network-icon"><Network size={18} /></div><div><h2>Local server binding</h2><p>Choose which network interface the selected server listens on.</p></div><span className="feature-chip">LOCAL</span></div>{profiles.length > 1 && <label className="field-label network-server-select">Server<select value={profile?.id || ''} onChange={(e) => { if (e.target.value) onChooseServer(e.target.value); }}><option value="">Select a server</option>{profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label>}
        <div className="network-choice"><div className="network-choice-copy"><span className="choice-radio"><i /></span><div><strong>Private interface</strong><span>Listen on this device’s selected private IPv4 address.</span></div></div><code>{localIp || 'No private IPv4 detected'}</code></div>
        <div className={`network-choice network-choice-all ${publicMode ? 'choice-active' : ''}`} onClick={() => setPublicMode((value) => !value)} role="button" tabIndex={0} onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') setPublicMode((value) => !value); }}><div className="network-choice-copy"><span className={`choice-radio ${publicMode ? 'choice-radio-on' : ''}`}><i /></span><div><strong>Expose to local network</strong><span>Listen on all IPv4 interfaces using 0.0.0.0.</span></div></div><span className={`switch ${publicMode ? 'switch-on' : ''}`}><i /></span></div>
        <label className="network-port-setting"><span>Server port</span><input type="number" min="1024" max="65535" value={selectedPort} onChange={(event) => setSelectedPort(Number(event.target.value))} disabled={!isNative} /><small>TCP · valid range 1024–65535</small></label>
        <div className="network-explain"><Info size={15} /><span>0.0.0.0 is a listening address, not a player address. Players on your LAN connect to <code>{localIp ? `${localIp}:${profile?.port || network?.recommendedPort || 25565}` : 'device-private-ip:port'}</code>. Public internet connectivity is not enabled by this setting.</span></div>
        <div className="network-card-footer"><span>{profile ? `Selected: ${profile.name} · port ${selectedPort}${running ? ' · running' : ''}` : 'No server selected'}</span><PrimaryButton onClick={() => void apply()} disabled={!profile || busy || !profile.installed || running || selectedPort < 1024 || selectedPort > 65535} icon={busy ? <LoaderCircle size={15} className="spin" /> : <Check size={15} />}>{busy ? 'Applying…' : 'Apply binding'}</PrimaryButton></div>
      </section>
      <section className="panel interfaces-card"><div className="panel-title-row"><div><h3>Detected interfaces</h3><p>IPv4 addresses on the active host</p></div><button className="icon-button" onClick={() => void onRefresh()} aria-label="Refresh interfaces"><RefreshCw size={15} /></button></div>{networkError ? <div className="inline-warning"><Info size={15} />{networkError}</div> : network?.interfaces.length ? <div className="interface-list">{network.interfaces.map((iface) => <div className="interface-row" key={`${iface.name}-${iface.ipv4}`}><span className={`interface-dot ${iface.private ? 'private-dot' : ''}`} /><span className="interface-name">{iface.name}</span><code>{iface.ipv4}</code><span className={`interface-tag ${iface.private ? 'tag-private' : ''}`}>{iface.private ? 'Private' : 'Other IPv4'}</span>{iface.selected && <span className="route-tag">Selected route</span>}</div>)}</div> : <div className="inline-empty">{isNative ? 'No IPv4 interfaces found. Check the device network state.' : 'Interface detection is available in the native installed app.'}</div>}</section>
      <section className="panel firewall-card"><div className="panel-title-row"><div><h3>Port and firewall</h3><p>Connections can be blocked even when a server is listening.</p></div><ShieldCheck size={17} /></div><div className="firewall-content"><div className="port-state"><span className="status-neutral-dot" /><div><strong>{profile ? `TCP ${profile.port}` : `Suggested TCP ${network?.recommendedPort || '—'}`}</strong><span>Availability check runs when selecting a port. Firewall status is not yet inspected.</span></div></div><div className="feature-later"><Info size={14} />No firewall rules will be changed automatically. Windows firewall integration needs explicit permission and is not implemented yet.</div></div></section>
    </div><aside className="network-aside"><section className="side-info-card"><span className="eyebrow">LAN CONNECTION</span><h3>Connection address</h3><p>Share this private address with devices on the same network.</p><div className="share-address"><code>{localIp && profile ? `${localIp}:${profile.port}` : 'Waiting for detection'}</code><button className="icon-button" onClick={() => void copyAddress()} disabled={!localIp || !profile} aria-label="Copy LAN address"><Copy size={15} /></button></div><span className="muted-footnote">Only reachable from your local network.</span></section><section className="side-info-card tunnel-card"><span className="eyebrow">REMOTE ACCESS</span><div className="tunnel-heading"><Radio size={17} /><h3>Cloudflare Tunnel</h3></div><span className="status-badge status-stopped"><i />Not configured</span><p>A normal Cloudflare HTTP tunnel does not expose Minecraft Java TCP traffic to arbitrary clients.</p><div className="tunnel-methods"><div><strong>Access TCP</strong><span>Players need client-side cloudflared authentication.</span></div><div><strong>Public TCP proxy</strong><span>Requires a separate proxy architecture or eligible Spectrum service.</span></div></div><span className="muted-footnote">No public connection is claimed until end-to-end connectivity is verified.</span></section></aside></div>
  </>;
}

function BackupsPage({ profiles, statuses, isNative, notify, updateProfile, record }: { profiles: ServerProfile[]; statuses: Record<string, ServerStatus>; isNative: boolean; notify: (message: string, tone?: 'success' | 'error' | 'info') => void; updateProfile: (id: string, patch: Partial<ServerProfile>) => void; record: (message: string, kind?: ActivityEntry['kind']) => void }) {
  const [loadingId, setLoadingId] = useState('');
  const createBackup = async (p: ServerProfile) => {
    if (!isNative) { notify('Backup creation requires the native Go host.'); return; }
    setLoadingId(p.id);
    try {
      const backup = await invoke<BackupRecord>('create_backup', { serverId: p.id, directory: p.directory });
      updateProfile(p.id, { backups: [backup, ...p.backups] }); record(`Backup created for ${p.name} · ${formatBytes(backup.sizeBytes)}`, 'success'); notify('Backup created successfully', 'success');
    } catch (error) { notify(errorMessage(error), 'error'); }
    finally { setLoadingId(''); }
  };
  const restore = async (p: ServerProfile, backup: BackupRecord) => {
    const dest = `${p.directory}-restored-${new Date().toISOString().replace(/[:.]/g, '-')}`;
    if (!window.confirm(`Restore this snapshot into a new folder?\n\n${dest}\n\nExisting server files will not be overwritten.`)) return;
    if (!isNative) { notify('Backup restore requires the native Go host.'); return; }
    setLoadingId(p.id);
    try {
      const result = await invoke<string>('restore_backup', { serverId: p.id, serverDirectory: p.directory, backupPath: backup.path, destination: dest, confirmed: true });
      record(`Backup restored for ${p.name} into a new directory`, 'success'); notify(`Backup restored to ${result}`, 'success');
    } catch (error) { notify(errorMessage(error), 'error'); }
    finally { setLoadingId(''); }
  };
  return <><PageHeading title="Backups" description="Create safe snapshots and restore them into a separate directory." action={<span className="policy-chip"><ShieldCheck size={14} />Non-destructive restore</span>} />{!profiles.length ? <div className="empty-panel list-empty"><div className="empty-icon"><Archive size={21} /></div><h2>No server data to back up</h2><p>Create a server before making a snapshot of its files.</p></div> : <div className="backup-server-list">{profiles.map((p) => <section className="panel backup-server-card" key={p.id}><div className="backup-server-header"><div className="server-name-cell"><SoftwareMark software={p.software} /><span><strong>{p.name}</strong><small>{p.directory}</small></span></div><QuietButton icon={loadingId === p.id ? <LoaderCircle size={15} className="spin" /> : <Plus size={15} />} disabled={!p.installed || !!statuses[p.id]?.running || !!loadingId} onClick={() => void createBackup(p)}>{loadingId === p.id ? 'Working…' : 'Create backup'}</QuietButton></div>{p.backups.length ? <div className="backup-history">{p.backups.map((b) => <div className="backup-row" key={b.path}><div className="backup-file-icon"><Archive size={16} /></div><div className="backup-file-meta"><strong>{b.path.split(/[\\/]/).pop()}</strong><span>{formatTime(b.createdAt)} <i>·</i> {formatBytes(b.sizeBytes)}</span></div><code className="backup-path">{b.path}</code><button className="button button-quiet" disabled={!!loadingId} onClick={() => void restore(p, b)}><Upload size={14} /> Restore to new folder</button></div>)}</div> : <div className="backup-none">No snapshots yet. Create one before making changes to this server.</div>}</section>)}</div>}<div className="local-data-note"><Info size={15} /><span>Logs, crash reports, caches, temporary downloads, and existing Horizon backup archives are excluded. Restores are extracted to a new directory and never overwrite the current server.</span></div></>;
}

function ConsolePage({ profiles, statuses, lines, isNative, notify, onClear }: { profiles: ServerProfile[]; statuses: Record<string, ServerStatus>; lines: ConsoleLine[]; isNative: boolean; notify: (message: string, tone?: 'success' | 'error' | 'info') => void; onClear: (serverId: string) => void }) {
  const [serverId, setServerId] = useState(profiles[0]?.id || '');
  const [query, setQuery] = useState('');
  const [command, setCommand] = useState('');
  const [paused, setPaused] = useState(false);
  const [frozenLines, setFrozenLines] = useState<ConsoleLine[] | null>(null);
  const [autoScroll, setAutoScroll] = useState(true);
  const outputRef = React.useRef<HTMLDivElement>(null);
  const current = profiles.find((p) => p.id === serverId);
  const running = !!statuses[serverId]?.running;
  const matchingLines = useMemo(() => lines.filter((line) => line.serverId === serverId && (!query || line.line.toLowerCase().includes(query.toLowerCase()))), [lines, serverId, query]);
  const visibleLines = paused && frozenLines ? frozenLines : matchingLines;
  useEffect(() => { if (autoScroll && !paused && outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }, [visibleLines.length, autoScroll, paused]);
  useEffect(() => { setPaused(false); setFrozenLines(null); }, [serverId]);
  const send = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!command.trim() || !serverId) return;
    if (!isNative || !running) { notify('Commands can only be sent to a running server in the native Go host.', 'error'); return; }
    try { await invoke('send_server_command', { serverId, command }); setCommand(''); }
    catch (error) { notify(errorMessage(error), 'error'); }
  };
  const copyVisible = async () => { await navigator.clipboard?.writeText(visibleLines.map((l) => `[${formatTime(l.timestamp)}] ${l.line}`).join('\n')); notify('Visible output copied', 'success'); };
  return <><PageHeading title="Console" description="Live process output and command input for a running server." action={<div className="console-status-group"><select value={serverId} onChange={(e) => setServerId(e.target.value)} aria-label="Select server">{profiles.length ? profiles.map((p) => <option value={p.id} key={p.id}>{p.name}</option>) : <option value="">No servers</option>}</select>{current && <StatusBadge running={running} />}</div>} />{!current ? <div className="empty-panel list-empty"><div className="empty-icon"><Terminal size={21} /></div><h2>No server selected</h2><p>Create a server profile to open its console.</p></div> : <div className="terminal-panel"><div className="terminal-toolbar"><div className="terminal-context"><span className={`terminal-live-dot ${running ? 'is-live' : ''}`} /><span>{current.name}</span><span className="terminal-separator">/</span><span>{running ? 'Live output' : 'Server stopped'}</span></div><div className="terminal-controls"><div className="console-search"><Search size={14} /><input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Filter output" /></div><button className="terminal-control" onClick={() => { if (paused) { setPaused(false); setFrozenLines(null); } else { setFrozenLines(matchingLines); setPaused(true); } }}><Pause size={14} />{paused ? 'Resume' : 'Pause'}</button><button className={`terminal-control ${autoScroll ? 'control-active' : ''}`} onClick={() => setAutoScroll((v) => !v)}><ArrowDownToLine size={14} />Auto-scroll</button><button className="terminal-control" onClick={copyVisible}><Copy size={14} />Copy</button><button className="terminal-control" onClick={() => { const blob = new Blob([visibleLines.map((l) => `[${formatTime(l.timestamp)}] [${l.stream}] ${l.line}`).join('\n')], { type: 'text/plain' }); const url = URL.createObjectURL(blob); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `${current.name.replace(/[^\w-]+/g, '-')}-console.txt`; anchor.click(); URL.revokeObjectURL(url); }}><Download size={14} />Download</button><button className="terminal-control" onClick={() => onClear(serverId)}><X size={14} />Clear view</button></div></div><div className="terminal-output" ref={outputRef} aria-live="off">{visibleLines.length ? (paused ? visibleLines : visibleLines).map((item, i) => <div className="terminal-line" key={`${item.timestamp}-${i}`}><time>{new Date(item.timestamp).toLocaleTimeString()}</time><span className={`stream-tag stream-${item.stream}`}>{item.stream === 'stderr' ? 'ERR' : 'OUT'}</span><span className={`line-content ${/\b(error|exception|failed|fatal)\b/i.test(item.line) ? 'line-error' : /\b(warn|warning)\b/i.test(item.line) ? 'line-warning' : ''}`}>{item.line}</span></div>) : <div className="terminal-empty"><Terminal size={18} /><span>{!isNative ? 'Live output is available in the native Go host.' : running ? 'Waiting for process output…' : 'Start this server to stream its console output.'}</span></div>}</div><form className="terminal-input-row" onSubmit={send}><span className="terminal-prompt">›</span><input value={command} onChange={(e) => setCommand(e.target.value)} placeholder={running ? 'Enter a server command…' : 'Commands are unavailable while the server is stopped'} disabled={!running || !isNative} /><button type="submit" className="terminal-send" disabled={!running || !isNative || !command.trim()}>Send <ArrowUpRight size={13} /></button></form><div className="terminal-footnote"><ShieldCheck size={13} />Input is passed only to this running server process. Horizon does not run shell commands here.</div></div>}</>;
}
function ActivityPage({ activity }: { activity: ActivityEntry[] }) {
  return <><PageHeading title="Activity & Logs" description="A local audit trail of server operations initiated in Horizon." />{activity.length ? <div className="audit-list">{activity.map((item) => <div className="audit-row" key={item.id}><span className={`activity-mark activity-${item.kind}`}><Check size={13} /></span><div><strong>{item.message}</strong><span>{item.kind === 'success' ? 'Completed' : item.kind === 'warning' ? 'Changed' : 'Recorded locally'}</span></div><time>{formatTime(item.at)}</time></div>)}</div> : <div className="empty-panel list-empty"><div className="empty-icon"><Activity size={21} /></div><h2>No activity yet</h2><p>Successful local operations will be recorded here.</p></div>}<div className="local-data-note"><ShieldCheck size={15} /><span>Activity is stored on this device. Session authorization tokens are never included in this activity history.</span></div></>;
}

function SettingsPage({ theme, setTheme, isNative, onRefresh }: { theme: 'dark' | 'light'; setTheme: React.Dispatch<React.SetStateAction<'dark' | 'light'>>; isNative: boolean; onRefresh: () => Promise<void> }) {
  const [defaultMemory, setDefaultMemory] = useState(Number(localStorage.getItem('horizon.default-memory') || '2048'));
  const [shutdownStatus, setShutdownStatus] = useState('');
  const shutdown = async () => {
    if (!window.confirm('Stop all managed servers and shut down this local host? Reopen the EXE to start Horizon again.')) return;
    setShutdownStatus('Shutting down the local host. Servers will be stopped gracefully.');
    try { await invoke('shutdown_host'); } catch (error) { setShutdownStatus(errorMessage(error)); }
  };
  const setMemory = (value: number) => { setDefaultMemory(value); persistValue('horizon.default-memory', String(value)); };
  return <><PageHeading title="Settings" description="Preferences and local host configuration." /><div className="settings-layout"><section className="settings-section"><div className="settings-section-heading"><h2>Appearance</h2><p>Choose the visual theme for this device.</p></div><div className="setting-row"><div className="setting-copy"><strong>Theme</strong><span>Applied immediately and saved locally.</span></div><div className="segmented-control"><button className={theme === 'dark' ? 'segment-active' : ''} onClick={() => setTheme('dark')}><Moon size={14} />Dark</button><button className={theme === 'light' ? 'segment-active' : ''} onClick={() => setTheme('light')}><Sun size={14} />Light</button></div></div></section><section className="settings-section"><div className="settings-section-heading"><h2>Server defaults</h2><p>Used as the initial value in the create server workflow.</p></div><div className="setting-row"><div className="setting-copy"><strong>Default JVM heap</strong><span>Maximum heap size. Actual process use may be higher.</span></div><div className="setting-value"><input type="number" min="512" max="32768" step="256" value={defaultMemory} onChange={(e) => setMemory(Number(e.target.value))} /><span>MB</span></div></div><div className="setting-row"><div className="setting-copy"><strong>Default network binding</strong><span>Private interface by default; all-interface binding requires a deliberate change.</span></div><span className="value-chip"><ShieldCheck size={14} />Private IPv4</span></div></section><section className="settings-section"><div className="settings-section-heading"><h2>Host capabilities</h2><p>Platform integrations currently available in this build.</p></div><div className="capability-list"><Capability label="Local profile storage" available description="Installed builds save profiles in app-private host storage, independent of the browser port." /><Capability label="Vanilla downloads and backups" available={isNative} description={isNative ? 'Verified official downloads, local file management, and offline backups.' : 'Requires the native Go host.'} /><Capability label="Minecraft process hosting" available={canHostServers} description={isAndroid ? androidRuntimeNotice : isNative ? 'Desktop Java from PATH, graceful shutdown, and streamed console output.' : 'Requires the installed Windows Go host.'} /><Capability label="Network interface detection" available={isNative} description="RFC 1918 IPv4 detection and route-aware preference." /><Capability label="Java runtime installer" available={false} description="Use Java from PATH until runtime management is implemented." /><Capability label="Firewall integration" available={false} description="No automatic firewall rule changes are performed." /></div><button className="button button-quiet settings-refresh" onClick={() => void onRefresh()}><RefreshCw size={14} /> Refresh host detection</button></section><section className="settings-section about-section"><div className="settings-section-heading"><h2>About Horizon</h2><p>Desktop and Android platform support are developed as separate integrations.</p></div><InfoRow label="Version" value="0.2.0 · Go migration preview" /><InfoRow label="Host" value={hostLabel} />{isNative && !isAndroid && <div className="host-shutdown-control"><QuietButton onClick={() => void shutdown()} disabled={!!shutdownStatus}>Shut down local host</QuietButton>{shutdownStatus && <p role="status">{shutdownStatus}</p>}</div>}<div className="config-warning"><Info size={15} /><span>{isAndroid ? 'The Kotlin foreground service and bounded wake lock cover file/download jobs only. Minecraft hosting requires a compatible Android JVM, which is not bundled. Android device behavior is not yet verified.' : 'The unsigned Windows preview uses a secured browser UI and desktop Java. Firewall automation, tray integration, tunnels, package catalogs, and Launcher IPC are not included.'}</span></div></section></div></>;
}
function Capability({ label, available, description }: { label: string; available: boolean; description: string }) { return <div className="capability-row"><span className={`capability-dot ${available ? 'capability-on' : ''}`} /><div><strong>{label}</strong><span>{description}</span></div><span className={`capability-status ${available ? 'status-on' : ''}`}>{available ? 'Available' : 'Planned'}</span></div>; }

function MobileNav({ page, setPage }: { page: Page; setPage: (page: Page) => void }) {
  const items: { label: Page; icon: typeof House }[] = [primaryPages[0], primaryPages[1], primaryPages[2], primaryPages[3], managementPages[2]];
  return <nav className="mobile-nav" aria-label="Mobile navigation">{items.map(({ label, icon: Icon }) => <button key={label} onClick={() => setPage(label)} className={page === label ? 'mobile-nav-active' : ''}><Icon size={19} /><span>{label === 'Plugins & Mods' ? 'Plugins' : label}</span></button>)}</nav>;
}

const root = createRoot(document.getElementById('root')!);
void initializeHost().then(() => root.render(<React.StrictMode><App /></React.StrictMode>)).catch((error: unknown) => root.render(<main className="host-startup-error"><h1>Could not connect to the local host</h1><p>{errorMessage(error)}</p><p>Existing preferences have not been overwritten. Reopen the app using its launcher.</p><button className="button button-quiet" onClick={() => location.reload()}>Retry connection</button></main>));
