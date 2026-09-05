import { describe, expect, it, beforeEach } from "vitest";
import {
  agentFixHeaders,
  clearAgentCreds,
  getAgentAPIKey,
  getAgentProvider,
  setAgentAPIKey,
  setAgentProvider,
} from "./agent-creds";

describe("agent-creds", () => {
  beforeEach(() => {
    clearAgentCreds();
  });

  it("stores provider and key in localStorage only", () => {
    setAgentProvider("cursor");
    setAgentAPIKey("sk-test");
    expect(getAgentProvider()).toBe("cursor");
    expect(getAgentAPIKey()).toBe("sk-test");
    expect(localStorage.getItem("xdlc_agent_provider")).toBe("cursor");
    expect(localStorage.getItem("xdlc_agent_api_key")).toBe("sk-test");
  });

  it("agentFixHeaders only when set", () => {
    expect(agentFixHeaders()).toEqual({});
    setAgentProvider("cursor");
    setAgentAPIKey("sk-test");
    expect(agentFixHeaders()).toEqual({
      "X-XDLC-Agent-Provider": "cursor",
      "X-XDLC-Agent-Key": "sk-test",
    });
  });

  it("clear removes both", () => {
    setAgentProvider("claude");
    setAgentAPIKey("x");
    clearAgentCreds();
    expect(getAgentProvider()).toBe("");
    expect(getAgentAPIKey()).toBe("");
  });
});
