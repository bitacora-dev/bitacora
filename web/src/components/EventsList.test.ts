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
});
