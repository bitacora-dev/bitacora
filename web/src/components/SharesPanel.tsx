import type { Inventory } from "../api";
import { formatBytes } from "../bytes";
import { useTranslation } from "../i18n";
import { formatAge } from "../relativeTime";

// Three inventories describe one subject: `share` is what is published,
// `share_usage` is how much of the disk it holds, and `user` is who can reach
// it. Splitting them into three generic lists would hand the operator a
// cross-reference exercise, so they are joined here into one row per share.
//
// The join key is `id`, never `name`. An NFS export is identified by its full
// path in `share_usage` while `share` names it by its basename, so joining on
// the readable name silently drops every NFS size.

export interface ShareAccess {
  readWrite: string[];
  readOnly: string[];
}

export interface ShareRow {
  id: string;
  name: string;
  protocol: string | null;
  path: string | null;
  mode: string | null;
  writable: boolean | null;
  // null is "not calculated yet", which is a real state: the collector runs
  // once a day. Substituting 0 would report an empty share.
  usedBytes: number | null;
  calculatedAt: string | null;
  access: ShareAccess;
}

export interface AccountRow {
  name: string;
  uid: string | null;
  readWrite: string[];
  readOnly: string[];
}

function text(attrs: Record<string, string> | undefined, key: string): string | null {
  const value = attrs?.[key];
  return typeof value === "string" && value.trim() !== "" ? value.trim() : null;
}

// The users collector omits `shares_rw`/`shares_ro` entirely when empty and
// joins the rest with bare commas.
function shareList(value: string | undefined): string[] {
  if (!value) return [];
  return value.split(",").map((entry) => entry.trim()).filter((entry) => entry !== "");
}

export function accountRows(users: Inventory | null): AccountRow[] {
  return (users?.items ?? []).map((item) => ({
    name: item.name || item.id,
    uid: text(item.attrs, "uid"),
    readWrite: shareList(item.attrs?.shares_rw),
    readOnly: shareList(item.attrs?.shares_ro),
  }));
}

// Inverts the user inventory: the agent reports "which shares can this
// account reach", and the panel reads "who can reach this share".
export function accessByShare(users: Inventory | null): Map<string, ShareAccess> {
  const access = new Map<string, ShareAccess>();
  const entry = (share: string) => {
    const existing = access.get(share) ?? { readWrite: [], readOnly: [] };
    access.set(share, existing);
    return existing;
  };
  for (const account of accountRows(users)) {
    for (const share of account.readWrite) entry(share).readWrite.push(account.name);
    for (const share of account.readOnly) entry(share).readOnly.push(account.name);
  }
  return access;
}

export function usedBytesByShare(usage: Inventory | null): Map<string, { usedBytes: number; calculatedAt: string | null }> {
  const sizes = new Map<string, { usedBytes: number; calculatedAt: string | null }>();
  for (const item of usage?.items ?? []) {
    // Number("") is 0, so the empty string has to be rejected before the
    // parse. An unreadable size drawn as an empty share is exactly the false
    // answer this panel exists to avoid.
    const raw = text(item.attrs, "used_bytes");
    if (raw === null) continue;
    const usedBytes = Number(raw);
    if (!Number.isFinite(usedBytes) || usedBytes < 0) continue;
    sizes.set(item.id, { usedBytes, calculatedAt: text(item.attrs, "calculated_at") });
  }
  return sizes;
}

export function shareRows(shares: Inventory | null, usage: Inventory | null, users: Inventory | null): ShareRow[] {
  const sizes = usedBytesByShare(usage);
  const access = accessByShare(users);
  return (shares?.items ?? []).map((item) => {
    const size = sizes.get(item.id);
    const writable = item.attrs?.writable;
    return {
      id: item.id,
      name: item.name || item.id,
      protocol: text(item.attrs, "protocol"),
      path: text(item.attrs, "path"),
      mode: text(item.attrs, "mode"),
      writable: writable === "true" ? true : writable === "false" ? false : null,
      usedBytes: size?.usedBytes ?? null,
      calculatedAt: size?.calculatedAt ?? null,
      access: access.get(item.id) ?? { readWrite: [], readOnly: [] },
    };
  });
}

interface Props {
  shares: Inventory | null;
  usage: Inventory | null;
  users: Inventory | null;
}

export default function SharesPanel({ shares, usage, users }: Props) {
  const { t, intlTag } = useTranslation();
  const rows = shareRows(shares, usage, users);
  const accounts = accountRows(users);
  if (rows.length === 0) return null;

  return (
    <article className="control-panel shares-panel">
      <div className="panel-title-row">
        <div>
          <h2>{t.sharesHeading}</h2>
          {shares && <p className="inventory-reported">{t.inventoryReportedAt(new Date(shares.reported_at).toLocaleString(intlTag))}</p>}
        </div>
        <span className="inventory-count">{rows.length}</span>
      </div>
      <ul className="share-list">
        {rows.map((row) => {
          const calculated = formatAge(row.calculatedAt, intlTag);
          return (
            <li key={row.id} className="share-row">
              <div className="share-row-heading">
                <strong>{row.name}</strong>
                {row.protocol && <span className="inventory-badge">{t.shareProtocol(row.protocol)}</span>}
                {row.mode && <span className="share-mode">{t.shareMode(row.mode)}</span>}
                {row.writable !== null && <span className="share-mode">{row.writable ? t.shareWritable : t.shareReadOnly}</span>}
              </div>
              <div className="share-size">
                {row.usedBytes === null ? (
                  <span className="share-size-pending">{t.shareUsagePending}</span>
                ) : (
                  <>
                    <strong>{formatBytes(row.usedBytes, intlTag)}</strong>
                    {/* The size is recomputed once a day. Printing it without
                        saying when it was measured presents yesterday's
                        number as today's. */}
                    <span>{calculated === null ? t.shareUsageCalculatedUnknown : t.shareUsageCalculated(calculated)}</span>
                  </>
                )}
              </div>
              {row.path && <p className="share-path">{row.path}</p>}
              <p className="share-access">
                {row.access.readWrite.length === 0 && row.access.readOnly.length === 0
                  ? t.shareAccessNone
                  : t.shareAccessSummary(row.access.readWrite.length, row.access.readOnly.length)}
              </p>
            </li>
          );
        })}
      </ul>
      {accounts.length > 0 && (
        // Account names and their permissions stay folded by default. They are
        // system usernames, and this panel is the surface that ADR-0023 will
        // one day show to a second person with access to this server.
        <details className="share-accounts">
          <summary>{t.shareAccountsToggle(accounts.length)}</summary>
          <p className="public-surface-note">{t.shareAccountsNote}</p>
          <ul>
            {accounts.map((account) => (
              <li key={account.name}>
                <div className="share-account-heading">
                  <strong>{account.name}</strong>
                  {account.uid && <span className="inventory-badge">{t.shareAccountUID(account.uid)}</span>}
                </div>
                <p>
                  {account.readWrite.length === 0 && account.readOnly.length === 0
                    ? t.shareAccountNoShares
                    : [
                        account.readWrite.length > 0 ? t.shareAccountReadWrite(account.readWrite.join(", ")) : null,
                        account.readOnly.length > 0 ? t.shareAccountReadOnly(account.readOnly.join(", ")) : null,
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                </p>
              </li>
            ))}
          </ul>
        </details>
      )}
    </article>
  );
}
