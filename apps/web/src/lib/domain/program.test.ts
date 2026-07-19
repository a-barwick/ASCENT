import { describe, expect, it } from "vitest";

import {
  advanceProgramClock,
  applyDecision,
  createInitialProgramState,
  resolveCommitment,
  windowLabel,
} from "./program";

describe("program decision loop", () => {
  it("opens the commitment gate after buying verification evidence", () => {
    const initial = createInitialProgramState();
    const resolved = applyDecision(initial, "verify");

    expect(resolved).not.toBe(initial);
    expect(initial.decisionResolved).toBeNull();
    expect(resolved).toMatchObject({
      phase: "commitment",
      decisionResolved: "verify",
      contingencyRemaining: 244_000_000,
      missionRisk: 21,
      confidence: 83,
      windowSecondsRemaining: 312 * 60,
    });
    expect(resolved.signoffs.find((signoff) => signoff.id === "assurance")?.status).toBe("go");
    expect(resolved.readiness.find((item) => item.id === "propulsion")).toMatchObject({
      value: 93,
      status: "go",
    });
  });

  it("makes preserving the window an explicit risk waiver", () => {
    const resolved = applyDecision(createInitialProgramState(), "waive");

    expect(resolved.windowSecondsRemaining).toBe(522 * 60);
    expect(resolved.missionRisk).toBe(56);
    expect(resolved.confidence).toBe(51);
    expect(resolved.signoffs.find((signoff) => signoff.id === "assurance")?.status).toBe("waived");
    expect(resolved.readiness.find((item) => item.id === "propulsion")?.status).toBe("waived");
  });

  it("turns a reserve acquisition into both reliability and rival pressure", () => {
    const resolved = applyDecision(createInitialProgramState(), "acquire");

    expect(resolved.contingencyRemaining).toBe(168_000_000);
    expect(resolved.windowSecondsRemaining).toBe(147 * 60);
    expect(resolved.missionRisk).toBe(11);
    expect(resolved.rivalDelayHours).toBe(19);
  });

  it("does not apply a second disposition to an already resolved decision", () => {
    const verified = applyDecision(createInitialProgramState(), "verify");

    expect(applyDecision(verified, "waive")).toBe(verified);
  });
});

describe("commitment outcome", () => {
  it("resolves a GO against the current mission-risk threshold", () => {
    const ready = applyDecision(createInitialProgramState(), "verify");

    expect(resolveCommitment(ready, "go", 0.2).outcome?.kind).toBe("failure");
    expect(resolveCommitment(ready, "go", 0.21).outcome).toMatchObject({
      kind: "success",
      contractDelta: 7_440_000_000,
    });
  });

  it("preserves the asset when the flight director scrubs", () => {
    const ready = applyDecision(createInitialProgramState(), "verify");
    const scrubbed = resolveCommitment(ready, "scrub", 1);

    expect(scrubbed.outcome).toMatchObject({
      kind: "scrubbed",
      contractDelta: -640_000_000,
      reputationDelta: -4,
      roll: null,
    });
    expect(scrubbed.contingencyRemaining).toBe(168_000_000);
    expect(scrubbed.committedSpend).toBe(4_302_000_000);
  });

  it("carries an accepted bearing anomaly into the failure investigation", () => {
    const waived = applyDecision(createInitialProgramState(), "waive");
    const failed = resolveCommitment(waived, "go", 0.1);

    expect(failed.outcome).toMatchObject({
      kind: "failure",
      headline: "Stage 2 shuts down at T+06:11",
    });
    expect(failed.outcome?.summary).toContain("bearing signature propagated");
  });

  it("requires a resolved readiness decision before commitment", () => {
    const initial = createInitialProgramState();

    expect(resolveCommitment(initial, "go", 1)).toBe(initial);
  });
});

describe("program clock", () => {
  it("formats the remaining window as a commitment countdown", () => {
    expect(windowLabel(522)).toBe("T−08:42");
    expect(windowLabel(0)).toBe("T−00:00");
    expect(windowLabel(-18)).toBe("T−00:00");
  });

  it("expires the corridor into a frozen stand-down outcome", () => {
    const initial = {
      ...createInitialProgramState(),
      windowSecondsRemaining: 2,
    };
    const finalSecond = advanceProgramClock(initial);
    const expired = advanceProgramClock(finalSecond);

    expect(finalSecond.windowSecondsRemaining).toBe(1);
    expect(finalSecond.outcome).toBeNull();
    expect(expired.windowSecondsRemaining).toBe(0);
    expect(expired.outcome).toMatchObject({
      kind: "scrubbed",
      headline: "ORPHEUS-1 misses the corridor",
    });
    expect(advanceProgramClock(expired)).toBe(expired);
  });
});
