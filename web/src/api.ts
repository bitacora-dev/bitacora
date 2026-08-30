// Types mirror internal/hubapi.Summary exactly — keep them in sync by
// hand; there are only three fields that matter and a generator would be
// overkill at this size.

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
  events: BitacoraEvent[];
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
