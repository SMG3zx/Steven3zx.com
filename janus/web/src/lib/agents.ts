/** An Agent as the gateway lists it. Matches the gateway's JSON. */
export type Agent = {
  id: string;
  name: string;
  tag: string;
  status: string;
  created_at: string;
  last_seen?: string;
  connected: boolean;
  paused: boolean;
  deployments: string[];
};

export type AgentState = {
  label: string;
  tone: "ok" | "muted";
  /** Whether the page should offer Pause; a paused Agent is offered Resume instead. */
  canPause: boolean;
};

export function agentState(agent: Agent): AgentState {
  if (agent.paused) {
    return { label: "Paused", tone: "muted", canPause: false };
  }
  if (agent.status === "pending") {
    return { label: "Waiting to join", tone: "muted", canPause: false };
  }
  if (agent.connected) {
    return { label: "Online", tone: "ok", canPause: true };
  }
  return { label: "Offline", tone: "muted", canPause: false };
}
