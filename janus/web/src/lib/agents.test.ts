import { describe, expect, test } from "bun:test";
import { agentState, type Agent } from "./agents";

const agent: Agent = {
  id: "a1",
  name: "laptop",
  tag: "tag:acct-1",
  status: "online",
  created_at: "2026-10-20T10:00:00Z",
  connected: true,
  paused: false,
  deployments: [],
};

describe("agentState", () => {
  test("reports a connected, running Agent as online", () => {
    expect(agentState(agent)).toEqual({ label: "Online", tone: "ok", canPause: true });
  });

  test("reports a paused Agent as paused and offers to resume it", () => {
    expect(agentState({ ...agent, paused: true })).toEqual({ label: "Paused", tone: "muted", canPause: false });
  });

  test("reports an Agent that has not joined yet as pending", () => {
    expect(agentState({ ...agent, status: "pending", connected: false })).toEqual({
      label: "Waiting to join",
      tone: "muted",
      canPause: false,
    });
  });

  test("reports an Agent that is enrolled but disconnected as offline", () => {
    expect(agentState({ ...agent, status: "offline", connected: false })).toEqual({
      label: "Offline",
      tone: "muted",
      canPause: false,
    });
  });
});
