import { useState } from "react";
import { createHost, type CreateHostResponse } from "../api";
import { agentSetupCommand } from "../enrollment";
import { useTranslation } from "../i18n";

interface AddServerPanelProps {
  onClose: () => void;
  onViewHost: (hostID: string) => void;
}

// Enrolls a new host from the browser (POST /v1/hosts) and shows the
// resulting credentials once. Nothing here persists the token: it lives in
// component state and disappears with the panel, which is the whole point —
// the hub can't hand it back either.
export default function AddServerPanel({ onClose, onViewHost }: AddServerPanelProps) {
  const { t } = useTranslation();
  const [host, setHost] = useState<CreateHostResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [copied, setCopied] = useState(false);
  const [name, setName] = useState("");

  const command = host ? agentSetupCommand({ hubURL: window.location.origin, host }) : "";

  const create = async () => {
    setPending(true);
    setError(null);
    try {
      setHost(await createHost(name.trim() || undefined));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setPending(false);
    }
  };

  const copy = async () => {
    try {
      // navigator.clipboard is undefined outside a secure context (plain
      // HTTP to a non-loopback host, which happens over Tailscale), so a
      // failure here must not look like the enrollment itself broke — the
      // command stays selectable on screen either way.
      await navigator.clipboard.writeText(command);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className="bg-neutral-900 border border-neutral-700 rounded p-4 flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">{t.addServerHeading}</h2>
        <button
          type="button"
          onClick={onClose}
          className="text-neutral-500 hover:text-neutral-300 py-2 px-2 -mr-2"
          aria-label={t.closeAria}
        >
          ✕
        </button>
      </div>

      {error && <div className="bg-red-950 border border-red-800 text-red-300 text-sm rounded p-3">{t.createServerError(error)}</div>}

      {!host && (
        <>
          <p className="text-sm text-neutral-400">{t.addServerIntro}</p>
          <label className="flex flex-col gap-1 text-sm text-neutral-400" htmlFor="server-name">
            {t.serverNameLabel}
            <input id="server-name" value={name} onChange={(event) => setName(event.target.value)} placeholder={t.serverNamePlaceholder} className="rounded bg-neutral-950 border border-neutral-700 px-3 py-2 text-neutral-100" />
          </label>
          <button
            type="button"
            onClick={create}
            disabled={pending}
            className="bg-sky-600 hover:bg-sky-500 disabled:opacity-50 rounded px-3 py-3"
          >
            {pending ? t.creatingServer : t.createServerButton}
          </button>
        </>
      )}

      {host && (
        <div className="flex flex-col gap-3">
          <div className="bg-amber-950 border border-amber-800 text-amber-200 text-sm rounded p-3">
            {t.tokenShownOnce}
          </div>

          <dl className="text-sm flex flex-col gap-2">
            <div>
              <dt className="text-neutral-500">{t.hostIdIssuedLabel}</dt>
              <dd className="font-mono break-all select-all">{host.host_id}</dd>
            </div>
            <div>
              <dt className="text-neutral-500">{t.ingestTokenLabel}</dt>
              <dd className="font-mono break-all select-all">{host.token}</dd>
            </div>
          </dl>

          <div className="flex flex-col gap-2">
            <p className="text-sm text-neutral-400">{t.runOnNewMachine}</p>
            <pre className="bg-neutral-950 border border-neutral-800 rounded p-3 text-xs overflow-x-auto select-all whitespace-pre">
              {command}
            </pre>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={copy}
                className="bg-neutral-800 hover:bg-neutral-700 rounded px-3 py-2 text-sm"
              >
                {copied ? t.copyDone : t.copyCommand}
              </button>
              <button
                type="button"
                onClick={() => onViewHost(host.host_id)}
                className="bg-sky-600 hover:bg-sky-500 rounded px-3 py-2 text-sm"
              >
                {t.viewHostButton}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
