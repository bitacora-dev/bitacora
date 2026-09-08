// Types mirror internal/hubapi.Summary exactly. Keep them in sync by hand;
// the shape is intentionally small enough that a generator would be noise.

export interface SeriesPoint {
  ts: string;
  value: number;
}

export interface EventSubject {
  kind: string;
  name: string;
  pid?: number;
}

export interface BitacoraEvent {
  id: string;
  ts: string;
  host_id: string;
  source: string;
  type: string;
  severity: "debug" | "info" | "notice" | "warn" | "error" | "critical";
  title: string;
  subject?: EventSubject;
}

export interface Summary {
  host_id: string;
  generated_at: string;
  window_secs: number;
  cpu: SeriesPoint[];
  memory: SeriesPoint[];
  memory_total_bytes: SeriesPoint[];
  memory_available_bytes: SeriesPoint[];
  memory_used_bytes: SeriesPoint[];
  memory_swap_total_bytes: SeriesPoint[];
  memory_swap_free_bytes: SeriesPoint[];
  events: BitacoraEvent[];
  jobs: Job[];
}

export interface Job {
  id: string;
  job_name: string;
  host_id: string;
  started_at: string;
  finished_at: string;
  duration_seconds: number;
  status: "success" | "warning" | "failed" | "timeout" | "killed" | "running";
  exit_code: number;
  stats?: Record<string, unknown>;
}

export interface EventHistory {
  host_id: string;
  from: string;
  to: string;
  limit: number;
  offset: number;
  total: number;
  events: BitacoraEvent[];
}

export interface EventHistoryQuery {
  from: string;
  to: string;
  severity?: BitacoraEvent["severity"] | "";
  type?: string;
  limit: number;
  offset: number;
}

export interface InventoryItem {
  id: string;
  name: string;
  attrs: Record<string, string>;
}

export interface Inventory {
  host_id: string;
  kind: string;
  reported_at: string;
  schema: number;
  items: InventoryItem[];
}

const TOKEN_KEY = "bitacora_device_token";

export function getDeviceToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setDeviceToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export interface PairResponse {
  code: string;
  expires_at: string;
  pair_path: string;
}

export async function fetchSummary(hostID: string, windowStr = "15m"): Promise<Summary> {
  const url = `/v1/summary?host_id=${encodeURIComponent(hostID)}&window=${encodeURIComponent(windowStr)}`;
  const token = getDeviceToken();
  const res = await fetch(url, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`GET ${url} -> ${res.status}: ${body}`);
  }
  return res.json();
}

export async function fetchEventHistory(hostID: string, query: EventHistoryQuery): Promise<EventHistory> {
  const params = new URLSearchParams({ host_id: hostID, from: query.from, to: query.to });
  if (query.severity) params.set("severity", query.severity);
  if (query.type) params.set("type", query.type);
  params.set("limit", String(query.limit));
  params.set("offset", String(query.offset));
  const url = `/v1/events?${params}`;
  const token = getDeviceToken();
  const res = await fetch(url, { headers: token ? { Authorization: `Bearer ${token}` } : undefined });
  if (!res.ok) throw new Error(`GET ${url} -> ${res.status}: ${await res.text()}`);
  return res.json();
}

// Returns null when a collector has not reported this inventory kind yet.
// Missing optional inventory is a normal capability state, not a hub error.
export async function fetchInventory(hostID: string, kind: string): Promise<Inventory | null> {
  const url = `/v1/inventory?host_id=${encodeURIComponent(hostID)}&kind=${encodeURIComponent(kind)}`;
  const token = getDeviceToken();
  const res = await fetch(url, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  });
  if (res.status === 404) return null;
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`GET ${url} -> ${res.status}: ${body}`);
  }
  return res.json();
}

export interface CreateHostResponse {
  host_id: string;
  token: string;
  created_at: string;
  host_id_path: string;
  token_path: string;
}

export interface Host {
  id: string;
  name?: string;
  hostname?: string;
  agent_version?: string;
  last_seen_at?: string;
}

export async function fetchHosts(): Promise<Host[]> {
  const token = getDeviceToken();
  const res = await fetch("/v1/hosts", {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`GET /v1/hosts -> ${res.status}: ${body}`);
  }
  const hosts: unknown = await res.json();
  return Array.isArray(hosts) ? hosts : [];
}

// Enrolls a new host and returns its ingest token. The plaintext token is
// only ever readable in this response — the hub stores an Argon2id hash and
// nothing can hand it back later, so the caller must show it immediately.
export async function createHost(name?: string, hostID?: string): Promise<CreateHostResponse> {
  const token = getDeviceToken();
  const res = await fetch("/v1/hosts", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify({ ...(hostID ? { host_id: hostID } : {}), ...(name ? { name } : {}) }),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`POST /v1/hosts -> ${res.status}: ${body}`);
  }
  return res.json();
}

export async function startPairing(): Promise<PairResponse> {
  const res = await fetch("/v1/devices/pair", { method: "POST" });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`POST /v1/devices/pair -> ${res.status}: ${body}`);
  }
  return res.json();
}

export async function claimPairing(code: string): Promise<{ token: string }> {
  const res = await fetch("/v1/devices/claim", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`POST /v1/devices/claim -> ${res.status}: ${body}`);
  }
  return res.json();
}
