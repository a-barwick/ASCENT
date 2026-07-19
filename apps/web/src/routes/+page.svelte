<script lang="ts">
  import { onMount } from "svelte";

  import DecisionConsole from "$lib/components/program/DecisionConsole.svelte";
  import PhaseRail from "$lib/components/program/PhaseRail.svelte";
  import ReadinessMatrix from "$lib/components/program/ReadinessMatrix.svelte";
  import RiskGauge from "$lib/components/program/RiskGauge.svelte";
  import Panel from "$lib/components/ui/Panel.svelte";
  import {
    advanceProgramClock,
    applyDecision,
    choiceById,
    createInitialProgramState,
    programAuthorization,
    programDirector,
    resolveCommitment,
    type CommitmentDirective,
    type DecisionChoiceId,
  } from "$lib/domain/program";

  let program = $state(createInitialProgramState());
  let selectedChoice = $state<DecisionChoiceId | null>(null);
  let scenarioRevision = $state(0);

  const activeChoice = $derived(choiceById(selectedChoice));
  const displayedRisk = $derived(
    !program.decisionResolved && activeChoice
      ? clamp(program.missionRisk + activeChoice.riskDelta, 4, 92)
      : program.missionRisk,
  );
  const displayedConfidence = $derived(
    !program.decisionResolved && activeChoice
      ? clamp(program.confidence + activeChoice.confidenceDelta, 5, 98)
      : program.confidence,
  );
  const displayedWindowSeconds = $derived(program.windowSecondsRemaining);
  const authorizationUsed = $derived(
    Math.round((program.committedSpend / programAuthorization) * 1_000) / 10,
  );
  const readinessAverage = $derived(
    Math.round(
      program.readiness.reduce((total, item) => total + item.value, 0) / program.readiness.length,
    ),
  );
  const openHolds = $derived(
    program.signoffs.filter((signoff) => signoff.status === "hold").length,
  );
  const rivalWindowHours = $derived(31 + program.rivalDelayHours);
  const missionClockLabel = $derived(program.outcome ? "Mission state" : "Primary window");
  const missionClockValue = $derived(
    program.outcome?.kind === "success"
      ? "ON STATION"
      : program.outcome?.kind === "failure"
        ? "CONTINGENCY"
        : program.outcome?.kind === "scrubbed"
          ? "RELEASED"
          : formatCountdown(displayedWindowSeconds),
  );
  const corridorState = $derived(
    program.outcome?.kind === "success"
      ? "USED"
      : program.outcome?.kind === "failure"
        ? "CLOSED"
        : program.outcome?.kind === "scrubbed"
          ? "RELEASED"
          : "RESERVED",
  );
  const corridorDetail = $derived(
    program.outcome?.kind === "success"
      ? "Sagan Range · launch completed"
      : program.outcome?.kind === "failure"
        ? "Sagan Range · contingency response"
        : program.outcome?.kind === "scrubbed"
          ? "Sagan Range · vehicle stood down"
          : `Sagan Range · closes ${formatCountdown(displayedWindowSeconds)}`,
  );
  const convoyState = $derived(
    program.outcome?.kind === "success"
      ? "EXPENDED"
      : program.outcome?.kind === "failure"
        ? "RECOVERY"
        : program.outcome?.kind === "scrubbed"
          ? "HOLD"
          : "ON PAD",
  );
  const convoyDetail = $derived(
    program.outcome?.kind === "success"
      ? "Load complete · support released"
      : program.outcome?.kind === "failure"
        ? "Pad and feed systems safed"
        : program.outcome?.kind === "scrubbed"
          ? "Return plan pending"
          : "Lattice Transit · seal chain intact",
  );
  const missionCondition = $derived(
    program.outcome?.kind === "success"
      ? "DELIVERED"
      : program.outcome?.kind === "failure"
        ? "RECOVERY"
        : program.outcome?.kind === "scrubbed"
          ? "STAND DOWN"
          : "73% GO",
  );
  const rangeCondition = $derived(
    program.outcome?.kind === "success"
      ? "COMPLETE"
      : program.outcome?.kind === "failure"
        ? "RESPONSE"
        : program.outcome?.kind === "scrubbed"
          ? "RELEASED"
          : "GREEN",
  );
  const rivalCondition = $derived(
    program.outcome?.kind === "success"
      ? `+${rivalWindowHours}H`
      : program.outcome
        ? "ADVANTAGE"
        : `+${rivalWindowHours}H`,
  );
  const missionConditionTone = $derived(
    program.outcome?.kind === "failure"
      ? "negative"
      : program.outcome?.kind === "scrubbed"
        ? "warning"
        : "positive",
  );
  const missionStateLabel = $derived(
    program.outcome?.kind === "success"
      ? "COMPLETE"
      : program.outcome?.kind === "failure"
        ? "INVESTIGATION"
        : program.outcome?.kind === "scrubbed"
          ? "STAND DOWN"
          : program.decisionResolved
            ? "GATE 4"
            : "HOLD",
  );
  const gateStatus = $derived(
    program.outcome
      ? program.outcome.kind
      : program.decisionResolved
        ? "Director poll open"
        : "Program hold",
  );

  onMount(() => {
    let observedAt = Date.now();
    let remainderMilliseconds = 0;
    const timer = window.setInterval(() => {
      const now = Date.now();
      const elapsedMilliseconds = Math.max(0, now - observedAt) + remainderMilliseconds;
      const elapsedSeconds = Math.floor(elapsedMilliseconds / 1_000);
      observedAt = now;
      remainderMilliseconds = elapsedMilliseconds - elapsedSeconds * 1_000;
      if (elapsedSeconds > 0) {
        program = advanceProgramClock(program, elapsedSeconds);
      }
    }, 250);
    return () => window.clearInterval(timer);
  });

  function authorizeDecision(): void {
    if (
      !selectedChoice ||
      program.decisionResolved ||
      program.outcome ||
      program.windowSecondsRemaining <= 0
    ) {
      return;
    }
    program = applyDecision(program, selectedChoice);
  }

  function commit(directive: CommitmentDirective): void {
    if (program.outcome || program.windowSecondsRemaining <= 0) return;
    program = resolveCommitment(program, directive, Math.random());
  }

  function resetScenario(): void {
    program = createInitialProgramState();
    selectedChoice = null;
    scenarioRevision += 1;
    window.scrollTo({ top: 0 });
  }

  function formatCountdown(totalSeconds: number): string {
    const safeSeconds = Math.max(0, Math.floor(totalSeconds));
    const hours = Math.floor(safeSeconds / 3_600);
    const minutes = Math.floor((safeSeconds % 3_600) / 60);
    const seconds = safeSeconds % 60;
    return `T−${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
  }

  function formatCredits(value: number): string {
    const absolute = Math.abs(value);
    const prefix = value < 0 ? "−" : "";
    if (absolute >= 1_000_000_000) {
      return `${prefix}${(absolute / 1_000_000_000).toFixed(2)}B CR`;
    }
    return `${prefix}${Math.round(absolute / 1_000_000)}M CR`;
  }

  function formatSignedCredits(value: number): string {
    return `${value >= 0 ? "+" : "−"}${formatCredits(Math.abs(value))}`;
  }

  function clamp(value: number, minimum: number, maximum: number): number {
    return Math.min(maximum, Math.max(minimum, value));
  }
</script>

<svelte:head>
  <title>ASCENT / ORPHEUS-1 Program Control</title>
  <meta
    name="description"
    content="Direct a flight-critical industrial program through readiness and commitment."
  />
</svelte:head>

<div class="program-shell">
  <header class="topbar">
    <a class="brand" href="/" aria-label="ASCENT program control home">
      <img src="/ascent-mark.svg" alt="" />
      <span>ASCENT</span>
    </a>
    <div class="program-identity">
      <span>Program control</span>
      <strong>ORPHEUS-1 / LC-01</strong>
    </div>
    <div
      class="commitment-clock"
      class:clock-critical={!program.outcome && displayedWindowSeconds < 10_800}
    >
      <span>{missionClockLabel}</span>
      <strong>{missionClockValue}</strong>
    </div>
    <nav aria-label="Program sections">
      <a href="#program">Program</a>
      <a href="#readiness">Readiness</a>
      <a href="#supply">Supply</a>
      <a href="#commitment">Commit</a>
    </nav>
    <div class="identity">
      <span class="connection__dot"></span>
      <span>{programDirector}</span>
      <strong>Scenario sandbox</strong>
    </div>
  </header>

  {#if program.outcome}
    <section class={`outcome-banner outcome-banner--${program.outcome.kind}`} aria-live="polite">
      <div>
        <span>{program.outcome.eyebrow}</span>
        <strong>{program.outcome.headline}</strong>
        <p>{program.outcome.summary}</p>
      </div>
      <div class="outcome-impact">
        <span>Contract impact</span>
        <strong>{formatSignedCredits(program.outcome.contractDelta)}</strong>
        <span>Reputation</span>
        <strong>
          {program.outcome.reputationDelta > 0 ? "+" : ""}{program.outcome.reputationDelta}
        </strong>
      </div>
      <button type="button" onclick={resetScenario}>Replay gate</button>
    </section>
  {/if}

  <main class="program-workspace">
    <section class="panel mission-hero" id="program" aria-labelledby="mission-title">
      <div class="mission-heading">
        <div>
          <span class="section-kicker">Mandate 07 / Cislunar infrastructure</span>
          <div class="mission-title-row">
            <h1 id="mission-title">ORPHEUS-1</h1>
            <span
              class:status-go={program.outcome?.kind === "success" ||
                (program.decisionResolved && !program.outcome)}
              class:status-failure={program.outcome?.kind === "failure"}
              class:status-scrubbed={program.outcome?.kind === "scrubbed"}
              class="mission-state"
            >
              {missionStateLabel}
            </span>
          </div>
          <p>
            Deploy Pioneer Tug to lunar orbit and commission the first autonomous cargo link before
            Vantage Orbital captures the customer’s expansion mandate.
          </p>
        </div>
        <div class="mandate-stakes">
          <span>Customer</span>
          <strong>Coalition Logistics Authority</strong>
          <small>6.80B CR award · 640M CR on-time bonus</small>
        </div>
      </div>

      <div class="mission-metrics">
        <div>
          <span>Committed program spend</span>
          <strong>{formatCredits(program.committedSpend)}</strong>
          <small>{authorizationUsed}% of authorization</small>
        </div>
        <div>
          <span>Contingency remaining</span>
          <strong>{formatCredits(program.contingencyRemaining)}</strong>
          <small
            >{Math.round((program.contingencyRemaining / 286_000_000) * 100)}% unallocated</small
          >
        </div>
        <div>
          <span>Modeled loss exposure</span>
          <strong>{program.missionRisk}%</strong>
          <small>{program.decisionResolved ? "After disposition" : "Before disposition"}</small>
        </div>
        <div>
          <span>Readiness confidence</span>
          <strong>{program.confidence}%</strong>
          <small>{readinessAverage}% system evidence</small>
        </div>
      </div>

      <PhaseRail current={program.phase} />

      <div class="mission-brief">
        <div>
          <span>Today’s commitment</span>
          <strong>Release the integrated stack—or protect the company from a bad launch.</strong>
        </div>
        <p>
          One unexplained bearing signature now sits between four years of industrial work and the
          most valuable cargo mandate in cislunar space. The trail begins with a supplier shortcut
          approved 118 days ago.
        </p>
      </div>
    </section>

    <Panel
      title="Turbopump bearing disposition"
      eyebrow="Active director decision"
      status={program.decisionResolved
        ? "Resolved"
        : program.outcome
          ? "Closed"
          : "HOLD / action due"}
      class="decision-panel"
    >
      {#key scenarioRevision}
        <DecisionConsole
          selected={selectedChoice}
          resolved={program.decisionResolved}
          locked={Boolean(program.outcome)}
          onselect={(choice) => (selectedChoice = choice)}
          onconfirm={authorizeDecision}
        />
      {/key}
    </Panel>

    <Panel
      title="Integrated readiness"
      eyebrow="Gate 4 evidence"
      status={`${readinessAverage}% / ${openHolds} holds`}
      class="readiness-panel"
    >
      <div class="readiness-summary" id="readiness">
        <div>
          <span>Gate state</span>
          <strong class:status-go={openHolds === 0}>
            {openHolds > 0 ? "HOLD" : "POLL OPEN"}
          </strong>
        </div>
        <div>
          <span>Systems above threshold</span>
          <strong>{program.readiness.filter((item) => item.value >= 85).length} / 6</strong>
        </div>
        <div>
          <span>Configuration</span>
          <strong>LC-01.1847</strong>
        </div>
        <p>
          Evidence changes only after a disposition is authorized. Waivers remain visible in the
          permanent configuration record.
        </p>
      </div>
      <ReadinessMatrix items={program.readiness} />
    </Panel>

    <Panel
      title="Mission exposure"
      eyebrow="Uncertainty model"
      status={activeChoice && !program.decisionResolved ? "Projected" : "Current"}
      class="risk-panel"
    >
      <RiskGauge risk={displayedRisk} confidence={displayedConfidence} />
      {#if activeChoice && !program.decisionResolved}
        <div class="projection-note">
          <span>Previewing</span>
          <strong>{activeChoice.label}</strong>
        </div>
      {/if}
      <dl class="exposure-list">
        <div>
          <dt>Vehicle loss claim</dt>
          <dd>{formatCredits(1_940_000_000)}</dd>
        </div>
        <div>
          <dt>Insurance premium</dt>
          <dd class="warning">+8.4%</dd>
        </div>
        <div>
          <dt>Delay tolerance</dt>
          <dd>36 hours</dd>
        </div>
      </dl>
      <div class="rival-card">
        <div>
          <span>Competitive pressure</span>
          <b>VANTAGE ORBITAL</b>
        </div>
        <strong>89%</strong>
        <small>ready</small>
        <p>
          Next launch in <b>{rivalWindowHours}h</b>. Its customer bid improves every hour your
          anomaly remains open.
        </p>
        {#if program.rivalDelayHours > 0}
          <span class="rival-impact">Reserve denied · rival slips +{program.rivalDelayHours}h</span>
        {/if}
      </div>
    </Panel>

    <Panel
      title="Critical path & capacity"
      eyebrow="Industrial dependency map"
      status="1 exposed chain"
      class="supply-panel"
    >
      <div class="critical-chain" id="supply">
        <article>
          <span class="chain-index">01</span>
          <div>
            <strong>Kestrel bearing / lot 77A</strong>
            <small>Supplier quality record</small>
          </div>
          <b class:status-go={program.decisionResolved === "acquire"}>
            {program.decisionResolved === "acquire"
              ? "REPLACED"
              : program.decisionResolved === "waive"
                ? "WAIVED"
                : program.decisionResolved === "verify"
                  ? "VERIFIED"
                  : "CONDITIONAL"}
          </b>
        </article>
        <article>
          <span class="chain-index">02</span>
          <div>
            <strong>Halcyon fuel turbopump</strong>
            <small>Stage-2 propulsion assembly</small>
          </div>
          <b
            class:status-go={program.outcome?.kind === "success" ||
              (program.decisionResolved && !program.outcome)}
          >
            {program.outcome?.kind === "success"
              ? "FLIGHT PROVEN"
              : program.outcome?.kind === "failure"
                ? "IMPOUNDED"
                : program.outcome?.kind === "scrubbed"
                  ? "REVIEW HOLD"
                  : program.decisionResolved
                    ? "RELEASED"
                    : "BLOCKED"}
          </b>
        </article>
        <article>
          <span class="chain-index">03</span>
          <div>
            <strong>Arcadia stage 2</strong>
            <small>Integrated flight vehicle</small>
          </div>
          <b
            class:status-go={program.outcome?.kind === "success" ||
              (program.decisionResolved && !program.outcome)}
          >
            {program.outcome?.kind === "success"
              ? "RECOVERED"
              : program.outcome?.kind === "failure"
                ? "ANOMALY HOLD"
                : program.outcome?.kind === "scrubbed"
                  ? "INTEGRATION"
                  : program.decisionResolved
                    ? "FLIGHT CONFIG"
                    : "GATE HOLD"}
          </b>
        </article>
        <article>
          <span class="chain-index">04</span>
          <div>
            <strong>ORPHEUS-1 / Pioneer Tug</strong>
            <small>6.80B CR customer mandate</small>
          </div>
          <b class:status-go={program.outcome?.kind === "success"}>
            {program.outcome?.kind === "success"
              ? "COMMISSIONED"
              : program.outcome
                ? "CLOSED"
                : "AWAITING GO"}
          </b>
        </article>
      </div>
      <div class="capacity-strip">
        <article>
          <span>Qualified reserve pump</span>
          <strong>{program.decisionResolved === "acquire" ? "ARCADIA LOCK" : "1 UNIT"}</strong>
          <small>Icarus Systems · Vantage competing</small>
        </article>
        <article>
          <span>Launch corridor</span>
          <strong>{corridorState}</strong>
          <small>{corridorDetail}</small>
        </article>
        <article>
          <span>Propellant convoy</span>
          <strong>{convoyState}</strong>
          <small>{convoyDetail}</small>
        </article>
      </div>
    </Panel>

    <Panel
      title="Program operations"
      eyebrow="Alerts, coordination & trace"
      status={`${program.timeline.length} events`}
      class="feed-panel"
    >
      <ol class="program-feed">
        {#each program.timeline.slice(0, 6) as entry}
          <li class={`feed-entry feed-entry--${entry.tone}`}>
            <time>{entry.time}</time>
            <div>
              <strong>{entry.title}</strong>
              <p>{entry.body}</p>
            </div>
          </li>
        {/each}
      </ol>
    </Panel>

    <Panel
      title="Flight director commitment"
      eyebrow="Gate 4 / accountable decision"
      status={gateStatus}
      class="gate-panel"
    >
      <div class="gate-console" id="commitment">
        <div class="gate-copy">
          <span>{program.decisionResolved ? "Readiness poll" : "Commitment locked"}</span>
          <strong>
            {program.outcome
              ? program.outcome.headline
              : program.decisionResolved
                ? "The evidence is recorded. The vehicle is yours."
                : "Resolve QA-1044 before polling the team."}
          </strong>
          <p>
            {program.outcome
              ? program.outcome.summary
              : "GO accepts the modeled mission risk and commits the full program. SCRUB preserves the asset, releases the window, and concedes first-mover advantage."}
          </p>
        </div>
        <div class="signoff-grid" aria-label="Readiness sign-offs">
          {#each program.signoffs as signoff}
            <div>
              <span>{signoff.role}</span>
              <strong>{signoff.operator}</strong>
              <b class={`signoff signoff--${signoff.status}`}>{signoff.status}</b>
            </div>
          {/each}
        </div>
        {#if program.outcome}
          <div class={`outcome-card outcome-card--${program.outcome.kind}`}>
            <span>{program.outcome.eyebrow}</span>
            <strong>{program.outcome.headline}</strong>
            {#if program.outcome.roll !== null}
              <small>
                Uncertainty draw {(program.outcome.roll * 100).toFixed(1)} / risk threshold
                {program.missionRisk}
              </small>
            {/if}
            <small>
              Contract {formatSignedCredits(program.outcome.contractDelta)} · Reputation
              {program.outcome.reputationDelta > 0 ? "+" : ""}{program.outcome.reputationDelta}
            </small>
            <button type="button" onclick={resetScenario}>Reset scenario</button>
          </div>
        {:else}
          <div class="director-actions">
            <button
              class="scrub-control"
              data-action="scrub"
              type="button"
              disabled={!program.decisionResolved}
              onclick={() => commit("scrub")}
            >
              <span>Protect the asset</span>
              <strong>SCRUB MISSION</strong>
              <small>Lose the window · preserve hardware</small>
            </button>
            <button
              class="go-control"
              data-action="go"
              type="button"
              disabled={!program.decisionResolved}
              onclick={() => commit("go")}
            >
              <span>Accept {program.missionRisk}% risk</span>
              <strong>ISSUE GO</strong>
              <small>{100 - program.missionRisk}% modeled success</small>
            </button>
          </div>
        {/if}
      </div>
    </Panel>
  </main>

  <footer class="mission-ticker" aria-label="Live mission conditions">
    <div>
      <span>{missionClockLabel}</span>
      <strong>{missionClockValue}</strong>
    </div>
    <div>
      <span>{program.outcome ? "Mission" : "Weather"}</span>
      <strong class={missionConditionTone}>{missionCondition}</strong>
    </div>
    <div>
      <span>Range</span>
      <strong class={missionConditionTone}>{rangeCondition}</strong>
    </div>
    <div>
      <span>Vantage</span>
      <strong>{rivalCondition}</strong>
    </div>
    <p>Local scenario model · stochastic commitment outcome · no backend commands</p>
  </footer>

  <nav class="mobile-nav" aria-label="Mobile program sections">
    <a href="#program">Program</a>
    <a href="#readiness">Ready</a>
    <a href="#supply">Supply</a>
    <a href="#commitment">Commit</a>
  </nav>
</div>
