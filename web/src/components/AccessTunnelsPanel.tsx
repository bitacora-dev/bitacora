import type { Inventory, InventoryItem } from "../api";
import { useTranslation } from "../i18n";
import { formatAge } from "../relativeTime";

// The operator reaches this server over Tailscale when the public route is
// down, so "are my tunnels up" is a question asked exactly when everything
// else is failing. It sits with the public-surface signals rather than under
// inventory: both describe how this host is connected to the outside world
// right now, and neither is a catalogue entry.
//
// A host that reports no tunnels renders nothing. The collector emits an
// empty snapshot when the privileged helper has not written a spool entry, and
// "no tunnels configured" must not be drawn as "every tunnel is down".

export type TunnelProtocol = "wireguard" | "tailscale" | "unknown";

export interface Tunnel {
  id: string;
  name: string;
  protocol: TunnelProtocol;
  // null is "not reported". It is not "down": an unanswered question and a
  // dead tunnel lead to very different decisions at 2 a.m.
  active: boolean | null;
  peer: string | null;
  endpoint: string | null;
  lastHandshake: string | null;
  state: string | null;
}

function text(attrs: Record<string, string> | undefined, key: string): string | null {
  const value = attrs?.[key];
  return typeof value === "string" && value.trim() !== "" ? value.trim() : null;
}

export function readTunnel(item: InventoryItem): Tunnel {
  const protocol = item.attrs?.protocol;
  const endpoint = text(item.attrs, "endpoint");
  return {
    id: item.id,
    name: item.name || item.id,
    protocol: protocol === "wireguard" || protocol === "tailscale" ? protocol : "unknown",
    active: item.attrs?.active === "true" ? true : item.attrs?.active === "false" ? false : null,
    peer: text(item.attrs, "peer"),
    // `wg show dump` writes the literal "(none)" for a peer that has never
    // been contacted. Echoing it would dress an absence up as an address.
    endpoint: endpoint === "(none)" ? null : endpoint,
    lastHandshake: text(item.attrs, "last_handshake"),
    state: text(item.attrs, "state"),
  };
}

export function readTunnels(inventory: Inventory | null): Tunnel[] {
  return (inventory?.items ?? []).map(readTunnel);
}

export function activeTunnelCount(tunnels: Tunnel[]): number {
  return tunnels.filter((tunnel) => tunnel.active === true).length;
}

interface Props {
  inventory: Inventory | null;
}

export default function AccessTunnelsPanel({ inventory }: Props) {
  const { t, intlTag } = useTranslation();
  const tunnels = readTunnels(inventory);
  if (tunnels.length === 0) return null;

  return (
    <article className="control-panel tunnels-panel">
      <div className="panel-title-row">
        <div>
          <h2>{t.tunnelsHeading}</h2>
          {inventory && <p className="inventory-reported">{t.inventoryReportedAt(new Date(inventory.reported_at).toLocaleString(intlTag))}</p>}
        </div>
        <span className="inventory-count">{t.tunnelsActiveCount(activeTunnelCount(tunnels), tunnels.length)}</span>
      </div>
      <ul className="tunnel-list">
        {tunnels.map((tunnel) => {
          const handshake = formatAge(tunnel.lastHandshake, intlTag);
          return (
            <li key={tunnel.id} className="tunnel-row">
              <div className="tunnel-row-heading">
                <strong>{tunnel.name}</strong>
                <span className="inventory-badge">{t.tunnelProtocol(tunnel.protocol)}</span>
                <span className={tunnelStateClass(tunnel.active)}>
                  {tunnel.active === true ? t.tunnelActive : tunnel.active === false ? t.tunnelInactive : t.tunnelStateUnreported}
                </span>
              </div>
              <dl className="tunnel-details">
                {tunnel.state && (
                  <div>
                    <dt>{t.tunnelBackendLabel}</dt>
                    <dd>{t.tunnelBackendState(tunnel.state)}</dd>
                  </div>
                )}
                {tunnel.peer && (
                  <div>
                    <dt>{t.tunnelPeerLabel}</dt>
                    <dd>{tunnel.peer}</dd>
                  </div>
                )}
                {tunnel.endpoint && (
                  <div>
                    <dt>{t.tunnelEndpointLabel}</dt>
                    <dd>{tunnel.endpoint}</dd>
                  </div>
                )}
                {tunnel.protocol === "wireguard" && (
                  <div>
                    <dt>{t.tunnelHandshakeLabel}</dt>
                    <dd>{handshake ?? t.tunnelHandshakeNever}</dd>
                  </div>
                )}
              </dl>
            </li>
          );
        })}
      </ul>
      {/* Without this, an idle-but-healthy WireGuard peer reads as a broken
          tunnel: the agent derives "active" from a handshake inside the last
          three minutes, and WireGuard only handshakes when traffic flows. */}
      <p className="public-surface-note">{t.tunnelsActivityNote}</p>
    </article>
  );
}

function tunnelStateClass(active: boolean | null): string {
  if (active === true) return "tunnel-state tunnel-state--active";
  if (active === false) return "tunnel-state tunnel-state--inactive";
  return "tunnel-state tunnel-state--unreported";
}
