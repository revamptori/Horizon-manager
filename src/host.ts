/** One command/event contract for a Go desktop host, Kotlin/JNI, or a capability-limited web preview. */
type Envelope<T> = { result?: T; error?: string };
type Pending = { resolve: (value: unknown) => void; reject: (reason: Error) => void; timer: number };
type HostEvent = { id: number; name: string; payload: unknown };
type EventBatch = { events: HostEvent[]; cursor: number; dropped: boolean };
type Capabilities = { version: string; platform: string; hostingAvailable: boolean; dataDirectory: string };

declare global {
  interface Window {
    __HORIZON_HOST__?: 'desktop' | 'android';
    HorizonAndroid?: { post: (request: string) => void };
    __horizonResolve?: (id: string, response: string) => void;
  }
}
export const isNative = !!window.__HORIZON_HOST__;
export const isAndroid = window.__HORIZON_HOST__ === 'android';
export const canHostServers = isNative && !isAndroid;
export let hostLabel = isAndroid ? 'Android · Go core' : isNative ? 'Go desktop host' : 'Browser preview';
export const androidRuntimeNotice = 'Minecraft hosting is unavailable in this Android preview. A compatible Android JVM is not bundled; desktop Java cannot run here. Profiles, verified downloads, and local backups are available.';
const pending = new Map<string, Pending>();
let token = '';
if (window.__HORIZON_HOST__ === 'desktop') {
  const parameters = new URLSearchParams(location.hash.slice(1));
  const incoming = parameters.get('horizon-token');
  if (incoming) { sessionStorage.setItem('horizon.session', incoming); history.replaceState(null, '', location.pathname + location.search); }
  token = sessionStorage.getItem('horizon.session') || '';
}
window.__horizonResolve = (id, serialized) => {
  const request = pending.get(id); if (!request) return;
  pending.delete(id); window.clearTimeout(request.timer);
  try {
    const envelope = JSON.parse(serialized) as Envelope<unknown>;
    if (envelope.error) request.reject(new Error(envelope.error)); else request.resolve(envelope.result);
  } catch { request.reject(new Error('The native host returned an invalid response.')); }
};

export async function invoke<T = unknown>(method: string, params: Record<string, unknown> = {}): Promise<T> {
  if (isAndroid) {
    if (!window.HorizonAndroid) throw new Error('The Android Go bridge is not connected.');
    return new Promise<T>((resolve, reject) => {
      const id = crypto.randomUUID();
      const timer = window.setTimeout(() => { pending.delete(id); reject(new Error('The native operation timed out. Check the notification and local files before retrying.')); }, 12 * 60 * 1000);
      pending.set(id, { resolve: (value) => resolve(value as T), reject, timer });
      try { window.HorizonAndroid!.post(JSON.stringify({ id, method, params })); }
      catch (error) { pending.delete(id); window.clearTimeout(timer); reject(error); }
    });
  }
  if (window.__HORIZON_HOST__ !== 'desktop') throw new Error('This action requires the installed Go host.');
  if (!token) throw new Error('This browser tab is not authorized. Reopen Horizon using the EXE launcher.');
  const response = await fetch('/api/call', {
    method: 'POST', cache: 'no-store', headers: { 'Content-Type': 'application/json', 'X-Horizon-Token': token },
    body: JSON.stringify({ method, params }),
  });
  if (!response.ok) throw new Error(`Local host request failed (HTTP ${response.status}). Relaunch Horizon if this session has ended.`);
  const envelope = await response.json() as Envelope<T>;
  if (envelope.error) throw new Error(envelope.error);
  return envelope.result as T;
}

const listeners = new Map<string, Set<(event: { payload: unknown }) => void>>();
let polling = false;
let cursor = 0;
async function poll() {
  if (!isNative || polling) return;
  polling = true;
  while (listeners.size) {
    let delay = 300;
    try {
      const batch = await invoke<EventBatch>('poll_events', { after: cursor });
      cursor = batch.cursor;
      for (const event of batch.events) for (const listener of listeners.get(event.name) || []) listener({ payload: event.payload });
      if (batch.dropped) window.dispatchEvent(new CustomEvent('horizon-host-warning', { detail: 'Some older console events were dropped while this view was disconnected.' }));
      if (batch.events.length === 500) delay = 0;
    } catch { delay = 2000; }
    await new Promise((resolve) => window.setTimeout(resolve, delay));
  }
  polling = false;
}
export async function listen<T>(name: string, callback: (event: { payload: T }) => void): Promise<() => void> {
  const typed = callback as (event: { payload: unknown }) => void;
  const group = listeners.get(name) || new Set(); group.add(typed); listeners.set(name, group); void poll();
  return () => { group.delete(typed); if (!group.size) listeners.delete(name); };
}

const preferenceKeys = ['horizon.server-profiles.v1', 'horizon.activity.v1', 'horizon.theme', 'horizon.default-memory'];
export async function initializeHost() {
  if (!isNative) return;
  const values = await invoke<Record<string, string>>('read_preferences');
  for (const key of preferenceKeys) { if (Object.hasOwnProperty.call(values, key)) localStorage.setItem(key, values[key]); else localStorage.removeItem(key); }
  const capabilities = await invoke<Capabilities>('host_capabilities');
  hostLabel = `${capabilities.platform === 'windows' ? 'Windows' : capabilities.platform === 'android' ? 'Android' : capabilities.platform} · Go core`;
}
let saveQueue = Promise.resolve();
export function persistValue(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { window.dispatchEvent(new CustomEvent('horizon-host-warning', { detail: 'Browser storage is full. Export or reduce local history.' })); }
  if (isNative) saveQueue = saveQueue.then(async () => {
    try { await invoke('write_preferences', { values: { [key]: value } }); }
    catch (error) { window.dispatchEvent(new CustomEvent('horizon-host-warning', { detail: `Could not save local preferences: ${String(error)}` })); }
  });
}
