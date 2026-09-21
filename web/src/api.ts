// Types mirror internal/hubapi.Summary exactly. Keep them in sync by hand;
// the shape is intentionally small enough that a generator would be noise.
// A field the hub sends and this file omits is dropped silently, with no
// compile error to catch it — declare what arrives even before it is rendered.

// Go's `omitempty` does not apply to time.Time, so a timestamp the producer
// never set arrives as the zero instant rather than being absent.
const ZERO_INSTANT_PREFIX = "0001-01-01";

export function hasInstant(value: string | null | undefined): value is string {
  return typeof value === "string" && value !== "" && !value.startsWith(ZERO_INSTANT_PREFIX);
}

export interface SeriesPoint {
  ts: string;
  value: number;
}

export interface CPUSeries {
  cpu: string;
  points: SeriesPoint[];
}

export interface TemperatureSeries {
  chip: string;
  sensor: string;
  points: SeriesPoint[];
}

// PublicSurface mirrors hubapi.PublicSurface: the signals the public_surface
// collector reports on internet-facing hosts.
//
// Read every field as "a series that may be empty", never as a number that
// may be zero. The collector only runs where the operator declared the host
// publicly exposed, so an empty series is the ordinary state, and drawing it
// as 0 would claim nobody is knocking — a reassurance nothing here supports.
export interface PublicSurface {
  // Cumulative failed-login count in the current auth log. It drops back on
  // logrotate, so it is context rather than an answer.
  ssh_failed_logins_total: SeriesPoint[];
  // The same counter differentiated by the hub: how fast attempts arrive.
  // This is what answers "am I being attacked right now".
  ssh_failed_logins_per_minute: SeriesPoint[];
  fail2ban_jails_total: SeriesPoint[];
  fail2ban_banned_total: SeriesPoint[];
  firewall_rules_total: SeriesPoint[];
  ovh_traffic_used_ratio: SeriesPoint[];
}

export interface EventSubject {
  kind: string;
  name: string;
  pid?: number;
}

// LogRef mirrors schema.LogRef: the log block and zero-based line an Event was
// derived from. logstore names each durable line "<block_id>:<line>", so a ref
// resolves to a single entry id without a second lookup.
export interface LogRef {
  block_id: string;
  line: number;
}

// JobLogRef mirrors schema.JobLogRef: the inclusive line range a Job's run
// produced inside one durable block.
export interface JobLogRef {
  block_id: string;
  from: number;
  to: number;
}

export interface BitacoraEvent {
  id: string;
  ts: string;
  // Go serializes time.Time even under `omitempty`, so an event the hub never
  // stamped arrives as the zero instant. Read it through hasInstant.
  ts_received?: string;
  host_id: string;
  source: string;
  type: string;
  severity: "debug" | "info" | "notice" | "warn" | "error" | "critical";
  title: string;
  subject?: EventSubject;
  attrs?: Record<string, unknown>;
  fingerprint?: string;
  log_refs?: LogRef[];
  schema?: number;
}

export interface Summary {
  host_id: string;
  generated_at: string;
  window_secs: number;
  cpu: SeriesPoint[];
  cpu_cores: CPUSeries[];
  temperatures: TemperatureSeries[];
  memory: SeriesPoint[];
  memory_total_bytes: SeriesPoint[];
  memory_available_bytes: SeriesPoint[];
  memory_used_bytes: SeriesPoint[];
  memory_swap_total_bytes: SeriesPoint[];
  memory_swap_free_bytes: SeriesPoint[];
  network_rx_bytes_per_second: SeriesPoint[];
  network_tx_bytes_per_second: SeriesPoint[];
  public_surface: PublicSurface;
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
  signal?: string;
  stats?: Record<string, unknown>;
  peer_host_id?: string;
  // What started this run — "systemd-timer" or "systemd-path" today.
  trigger?: string;
  // When the trigger is due again. Absent runs arrive as the zero instant, so
  // read it through hasInstant.
  next_expected?: string;
  log_refs?: JobLogRef[];
  schema?: number;
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

export interface LogEntry {
  id: string;
  ts: string;
  host_id: string;
  source: string;
  unit?: string;
  message: string;
}

export interface LogHistory {
  host_id: string;
  from: string;
  to: string;
  limit: number;
  offset: number;
  total: number;
  entries: LogEntry[];
}

export interface LogHistoryQuery {
  from: string;
  to: string;
  text?: string;
  source?: string;
  unit?: string;
  // A single durable block id, as carried by an Event's or a Job's log_refs.
  block?: string;
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

export type PackageOperation = "REFRESH_PACKAGE_CACHE" | "APPLY_PENDING_PACKAGE_UPDATES";

export interface ActionToken {
  request_id: string;
  action_token: string;
  expires_at: string;
}

export interface JobOutputLine {
  job_id: string;
  sequence: number;
  ts: string;
  stream: string;
  message: string;
}

export interface JobPoll {
  job: Job;
  lines: JobOutputLine[];
  next_after: number;
  complete: boolean;
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

export async function fetchLogHistory(hostID: string, query: LogHistoryQuery): Promise<LogHistory> {
  const params = new URLSearchParams({ host_id: hostID, from: query.from, to: query.to, limit: String(query.limit), offset: String(query.offset) });
  if (query.text) params.set("text", query.text);
  if (query.source) params.set("source", query.source);
  if (query.unit) params.set("unit", query.unit);
  if (query.block) params.set("block", query.block);
  const url = `/v1/logs?${params}`;
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

async function actionRequest<T>(path: string, body?: Record<string, string>): Promise<T> {
  const token = getDeviceToken();
  const res = await fetch(path, {
    method: body ? "POST" : "GET",
    headers: {
      ...(body ? { "Content-Type": "application/json" } : {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`);
  return res.json();
}

export async function fetchActionAvailability(hostID: string): Promise<boolean> {
  try {
    await actionRequest(`/v1/actions/package-operations?host_id=${encodeURIComponent(hostID)}`);
    return true;
  } catch {
    return false;
  }
}

export function issueActionToken(hostID: string, operation: PackageOperation): Promise<ActionToken> {
  return actionRequest("/v1/actions/package-operations/token", { host_id: hostID, operation });
}

export function confirmAction(hostID: string, operation: PackageOperation, issued: ActionToken): Promise<{ status: string }> {
  return actionRequest("/v1/actions/package-operations/confirm", {
    host_id: hostID,
    operation,
    request_id: issued.request_id,
    action_token: issued.action_token,
  });
}

export function fetchJob(hostID: string, jobID: string, after: number): Promise<JobPoll> {
  return actionRequest(`/v1/jobs/${encodeURIComponent(jobID)}?host_id=${encodeURIComponent(hostID)}&after=${after}`);
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
