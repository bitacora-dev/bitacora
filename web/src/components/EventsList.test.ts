import { describe, expect, it } from "vitest";
import type { BitacoraEvent } from "../api";
import { eventDetails } from "./EventsList";

const event: BitacoraEvent = {
  id: "event-1",
  ts: "2026-09-19T11:31:18Z",
  host_id: "host-1",
  source: "ingest",
  type: "ingest.validation_rejected",
  severity: "warn",
  title: "Rejected invalid metric during ingest",
};

describe("eventDetails", () => {
  it("keeps the rejected data name and reason first for an ingest validation event", () => {
    expect(eventDetails({
      ...event,
      attrs: { data_kind: "metric", data_name: "bitacora_net_rx_bytes_per_second", reason: "name must end in _bytes" },
    }).slice(0, 2)).toEqual([
      { key: "data_name", value: "bitacora_net_rx_bytes_per_second" },
      { key: "reason", value: "name must end in _bytes" },
    ]);
  });

  it("bounds hostile values, collapses line breaks, and limits attribute count", () => {
    const details = eventDetails({
      ...event,
      attrs: {
        data_name: "metric\nwith\r\nnewlines",
        reason: "x".repeat(8_000),
        alpha: "a",
        beta: "b",
        gamma: "c",
        ["aaa\n".repeat(100)]: "included with a bounded key",
      },
    });

    expect(details).toHaveLength(4);
    expect(details[0].value).toBe("metric with newlines");
    expect(details[1].value).toHaveLength(160);
    expect(details[1].value.endsWith("…")).toBe(true);
    expect(details.some(({ value }) => /[\r\n]/.test(value))).toBe(false);
    expect(details.every(({ key }) => key.length <= 64 && !/[\r\n]/.test(key))).toBe(true);
  });

  it("keeps subject fields in the same bounded detail view", () => {
    expect(eventDetails({ ...event, subject: { kind: "container", name: "api", pid: 42 } })).toEqual([
      { key: "subject.kind", value: "container" },
      { key: "subject.name", value: "api" },
      { key: "subject.pid", value: "42" },
    ]);
  });

  it("returns no details for an event without attributes or subject", () => {
    expect(eventDetails(event)).toEqual([]);
  });

  it("surfaces the provenance the hub sends: when it arrived, its fingerprint and its log coordinates", () => {
    expect(eventDetails({
      ...event,
      ts_received: "2026-09-19T11:31:20Z",
      fingerprint: "e3b0c44298fc",
      log_refs: [{ block_id: "block-a", line: 5 }, { block_id: "block-a", line: 7 }],
    })).toEqual([
      { key: "ts_received", value: "2026-09-19T11:31:20Z" },
      { key: "fingerprint", value: "e3b0c44298fc" },
      { key: "log_refs", value: "block-a:5, block-a:7" },
    ]);
  });

  it("marks a longer ref list as truncated instead of implying it is complete", () => {
    const details = eventDetails({
      ...event,
      log_refs: [0, 1, 2, 3].map((line) => ({ block_id: "block-a", line })),
    });
    expect(details).toEqual([{ key: "log_refs", value: "block-a:0, block-a:1, block-a:2, …" }]);
  });

  it("hides a reception time that adds nothing, and the zero instant Go sends for an unset one", () => {
    expect(eventDetails({ ...event, ts_received: event.ts })).toEqual([]);
    expect(eventDetails({ ...event, ts_received: "0001-01-01T00:00:00Z" })).toEqual([]);
  });
});
