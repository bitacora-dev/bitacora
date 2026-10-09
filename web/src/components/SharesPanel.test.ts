import { describe, expect, it } from "vitest";
import type { Inventory } from "../api";
import { accessByShare, accountRows, shareRows, usedBytesByShare } from "./SharesPanel";

function inventory(kind: string, items: Inventory["items"]): Inventory {
  return { host_id: "host-a", kind, reported_at: "2026-09-21T12:00:00Z", schema: 1, items };
}

const shares = inventory("share", [
  { id: "multimedia", name: "Multimedia", attrs: { protocol: "smb", path: "/mnt/user/multimedia", mode: "private", writable: "true" } },
  // NFS identifies an export by its full path while naming it by the
  // basename. Both spellings appear in this fixture on purpose.
  { id: "/srv/backups", name: "backups", attrs: { protocol: "nfs", path: "/srv/backups", mode: "public", writable: "false" } },
]);

const usage = inventory("share_usage", [
  { id: "multimedia", name: "multimedia", attrs: { used_bytes: "4200000000", calculated_at: "2026-09-21T03:00:00Z" } },
  { id: "/srv/backups", name: "/srv/backups", attrs: { used_bytes: "900000000", calculated_at: "2026-09-21T03:00:00Z" } },
]);

const users = inventory("user", [
  { id: "nacho", name: "nacho", attrs: { uid: "1000", shares_rw: "multimedia,documentos" } },
  { id: "invitado", name: "invitado", attrs: { uid: "1001", shares_ro: "multimedia" } },
  { id: "sinacceso", name: "sinacceso", attrs: { uid: "1002" } },
]);

describe("usedBytesByShare", () => {
  it("keys sizes by id, which is how share_usage identifies an NFS export", () => {
    expect(usedBytesByShare(usage).get("/srv/backups")).toEqual({ usedBytes: 900000000, calculatedAt: "2026-09-21T03:00:00Z" });
  });

  it("drops an unparseable size rather than charting NaN", () => {
    expect(usedBytesByShare(inventory("share_usage", [{ id: "a", name: "a", attrs: { used_bytes: "" } }])).size).toBe(0);
  });
});

describe("accountRows", () => {
  it("splits the comma-joined share lists the users collector emits", () => {
    expect(accountRows(users)[0]).toEqual({ name: "nacho", uid: "1000", readWrite: ["multimedia", "documentos"], readOnly: [] });
  });

  // The collector omits shares_rw/shares_ro entirely when a user reaches no
  // share, so the absent key has to read as an empty list.
  it("reads an account with no declared shares as having none", () => {
    expect(accountRows(users)[2]).toEqual({ name: "sinacceso", uid: "1002", readWrite: [], readOnly: [] });
  });

  it("returns nothing when the user inventory was never reported", () => {
    expect(accountRows(null)).toEqual([]);
  });
});

describe("accessByShare", () => {
  it("inverts account permissions into per-share access", () => {
    expect(accessByShare(users).get("multimedia")).toEqual({ readWrite: ["nacho"], readOnly: ["invitado"] });
  });

  // valid users and write list are independent in smb.conf, so an account can
  // appear in one list and not the other without that being a contradiction.
  it("keeps a share only one account can write", () => {
    expect(accessByShare(users).get("documentos")).toEqual({ readWrite: ["nacho"], readOnly: [] });
  });
});

describe("shareRows", () => {
  it("joins size and access onto each share", () => {
    const rows = shareRows(shares, usage, users);
    expect(rows[0]).toEqual({
      id: "multimedia",
      name: "Multimedia",
      protocol: "smb",
      path: "/mnt/user/multimedia",
      mode: "private",
      writable: true,
      usedBytes: 4200000000,
      calculatedAt: "2026-09-21T03:00:00Z",
      access: { readWrite: ["nacho"], readOnly: ["invitado"] },
    });
  });

  // Joining on `name` would silently lose every NFS size: share_usage keys
  // the export by its full path while share names it by the basename.
  it("matches an NFS export by id, not by its readable name", () => {
    expect(shareRows(shares, usage, users)[1].usedBytes).toBe(900000000);
  });

  // The size collector runs once a day and skips a share it could not walk.
  // A missing row is "not calculated", never an empty share.
  it("leaves the size null when nothing has been calculated yet", () => {
    const rows = shareRows(shares, null, users);
    expect(rows[0].usedBytes).toBeNull();
    expect(rows[0].calculatedAt).toBeNull();
  });

  // smb.conf only sets mode and writable when the section declares them.
  it("does not invent a permission the share configuration never declared", () => {
    const rows = shareRows(inventory("share", [{ id: "scratch", name: "scratch", attrs: { protocol: "smb" } }]), null, null);
    expect(rows[0].mode).toBeNull();
    expect(rows[0].writable).toBeNull();
    expect(rows[0].path).toBeNull();
  });

  it("survives the null item list an empty share snapshot produces", () => {
    expect(shareRows({ ...shares, items: null as unknown as Inventory["items"] }, usage, users)).toEqual([]);
  });
});
