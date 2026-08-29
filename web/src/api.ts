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

export async function fetchSummary(hostID: string, windowStr = "15m"): Promise<Summary> {
  const url = `/v1/summary?host_id=${encodeURIComponent(hostID)}&window=${encodeURIComponent(windowStr)}`;
  const res = await fetch(url);
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`GET ${url} -> ${res.status}: ${body}`);
  }
  return res.json();
}
