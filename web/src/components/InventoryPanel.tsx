import type { Inventory } from "../api";
import { useTranslation } from "../i18n";

interface InventoryPanelProps {
  inventory: Inventory | null;
  kind: "disk" | "package_update";
}

const diskAttributes = ["device", "model", "serial", "fstype", "capacity_bytes", "used_bytes", "available_bytes"] as const;
const updateAttributes = ["source", "installed_version", "candidate_version", "current_digest", "registry_digest", "arch", "repo", "cache_age_seconds"] as const;

function parseBytes(value: string | undefined): number | null {
  if (!value) return null;
  const bytes = Number(value);
  return Number.isFinite(bytes) && bytes >= 0 ? bytes : null;
}

export default function InventoryPanel({ inventory, kind }: InventoryPanelProps) {
  const { t, intlTag } = useTranslation();
  const isDisk = kind === "disk";
  const title = isDisk ? t.disksHeading : t.updatesHeading;
  const attributes = isDisk ? diskAttributes : updateAttributes;

  return (
    <article className="control-panel inventory-panel">
      <div className="panel-title-row">
        <div>
          <h2>{title}</h2>
          {inventory && <p className="inventory-reported">{t.inventoryReportedAt(new Date(inventory.reported_at).toLocaleString(intlTag))}</p>}
        </div>
        <span>{inventory?.items.length ?? 0}</span>
      </div>
      {!inventory ? (
        <p className="inventory-empty">{t.inventoryPending}</p>
      ) : inventory.items.length === 0 ? (
        <p className="inventory-empty">{isDisk ? t.disksEmpty : t.updatesEmpty}</p>
      ) : (
        <ul className="inventory-list">
          {inventory.items.map((item) => (
            <li key={item.id}>
              <strong>{item.name}</strong>
              <dl>
                {attributes.map((attribute) => {
                  const value = item.attrs[attribute];
                  if (!value) return null;
                  const bytes = attribute.endsWith("_bytes") ? parseBytes(value) : null;
                  return (
                    <div key={attribute}>
                      <dt>{t.inventoryAttribute(attribute)}</dt>
                      <dd>{bytes === null ? value : t.inventoryBytes(bytes, intlTag)}</dd>
                    </div>
                  );
                })}
              </dl>
            </li>
          ))}
        </ul>
      )}
    </article>
  );
}
