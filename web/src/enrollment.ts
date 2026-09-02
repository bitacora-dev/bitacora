import type { CreateHostResponse } from "./api";

// Builds the exact shell snippet an operator pastes on the new machine.
// It lives apart from the component so the one thing that must not be
// wrong — the command itself — is a pure function with its own tests.

function dirname(path: string): string {
  const idx = path.lastIndexOf("/");
  if (idx <= 0) return "/";
  return path.slice(0, idx);
}

// Single-quoting is what makes it safe to paste a value the shell would
// otherwise interpret. Inside single quotes only the quote itself needs
// escaping, via the standard '\'' idiom.
function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

export interface AgentSetupInput {
  hubURL: string;
  host: CreateHostResponse;
}

// The agent generates its own host_id on first run (ADR-0004), so seeding
// the file the hub issued is not optional: without it the agent would send
// batches under a different host_id than the token is bound to, and the hub
// would reject every one of them with 403.
export function agentSetupCommand({ hubURL, host }: AgentSetupInput): string {
  const hostIDDir = dirname(host.host_id_path);
  const tokenDir = dirname(host.token_path);

  return [
    `install -d -m 0755 ${hostIDDir}`,
    `printf '%s\\n' ${shellQuote(host.host_id)} > ${host.host_id_path}`,
    `install -d -m 0750 ${tokenDir}`,
    `printf '%s\\n' ${shellQuote(host.token)} > ${host.token_path}`,
    `chmod 0600 ${host.token_path}`,
    `bitacora-agent -hub-url=${shellQuote(hubURL)} -token-file=${host.token_path}`,
  ].join("\n");
}
