export type ProgramPhase = "mandate" | "build" | "integration" | "readiness" | "commitment";
export type ReadinessStatus = "go" | "watch" | "hold" | "waived";
export type SignoffStatus = "go" | "hold" | "pending" | "waived" | "conditional";
export type DecisionChoiceId = "verify" | "waive" | "acquire";
export type CommitmentDirective = "go" | "scrub";
export type OutcomeKind = "success" | "failure" | "scrubbed";

export interface Phase {
  id: ProgramPhase;
  label: string;
  detail: string;
}

export interface ReadinessItem {
  id: string;
  label: string;
  owner: string;
  value: number;
  status: ReadinessStatus;
  note: string;
}

export interface Signoff {
  id: string;
  role: string;
  operator: string;
  status: SignoffStatus;
}

export interface TimelineEntry {
  id: string;
  time: string;
  title: string;
  body: string;
  tone: "neutral" | "positive" | "warning" | "critical";
}

export interface DecisionChoice {
  id: DecisionChoiceId;
  label: string;
  strategy: string;
  description: string;
  costDelta: number;
  windowDeltaMinutes: number;
  riskDelta: number;
  confidenceDelta: number;
  rivalDelayHours: number;
  consequence: string;
}

export interface MissionOutcome {
  kind: OutcomeKind;
  eyebrow: string;
  headline: string;
  summary: string;
  contractDelta: number;
  reputationDelta: number;
  roll: number | null;
}

export interface ProgramState {
  phase: ProgramPhase;
  contingencyRemaining: number;
  committedSpend: number;
  missionRisk: number;
  confidence: number;
  windowSecondsRemaining: number;
  rivalDelayHours: number;
  decisionResolved: DecisionChoiceId | null;
  readiness: ReadinessItem[];
  signoffs: Signoff[];
  timeline: TimelineEntry[];
  outcome: MissionOutcome | null;
}

export const phases: Phase[] = [
  { id: "mandate", label: "Mandate", detail: "Award secured" },
  { id: "build", label: "Build", detail: "Hardware complete" },
  { id: "integration", label: "Integrate", detail: "Stack verified" },
  { id: "readiness", label: "Readiness", detail: "Gate 4 active" },
  { id: "commitment", label: "Commit", detail: "Director decision" },
];

export const programAuthorization = 4_528_000_000;
export const programDirector = "Mara Vance";

export const decisionChoices: DecisionChoice[] = [
  {
    id: "verify",
    label: "Run an instrumented spin test",
    strategy: "Buy information",
    description:
      "Hold the stack for 3h 30m and reproduce the load profile. Preserve the launch day, but give Vantage time to improve its customer bid.",
    costDelta: -42_000_000,
    windowDeltaMinutes: -210,
    riskDelta: -13,
    confidenceDelta: 15,
    rivalDelayHours: 0,
    consequence: "Best balance of evidence, schedule, and program credibility.",
  },
  {
    id: "waive",
    label: "Accept the supplier disposition",
    strategy: "Protect the window",
    description:
      "Classify the signature as non-propagating and fly the installed unit. No schedule loss, but you personally own the unexplained anomaly.",
    costDelta: 0,
    windowDeltaMinutes: 0,
    riskDelta: 22,
    confidenceDelta: -17,
    rivalDelayHours: 0,
    consequence: "Preserves the 640M CR on-time bonus with the widest failure tail.",
  },
  {
    id: "acquire",
    label: "Seize the last qualified reserve",
    strategy: "Spend for control",
    description:
      "Outbid Vantage for Icarus Systems’ final flight unit and execute an expedited swap. The rival loses its spare and slips if its own pump fails inspection.",
    costDelta: -118_000_000,
    windowDeltaMinutes: -375,
    riskDelta: -23,
    confidenceDelta: 22,
    rivalDelayHours: 19,
    consequence: "Most reliable path—and an openly hostile capacity play.",
  },
];

export const evidence = {
  engineering: {
    label: "Engineering",
    headline: "A repeatable 0.7g radial signature appears above 31,000 RPM.",
    points: [
      "Signature is 18% below the hard reject limit.",
      "No metal was found in the lubrication filter.",
      "The digital twin cannot explain the third harmonic.",
    ],
    signal: [18, 22, 21, 28, 36, 44, 61, 53, 67],
  },
  supplier: {
    label: "Supplier",
    headline: "Kestrel Precision changed its finishing process 11 weeks ago.",
    points: [
      "This bearing lot is the first produced after the process change.",
      "Two inspection records were closed by the same quality lead.",
      "Supplier offers a written disposition, not a replacement guarantee.",
    ],
    signal: [84, 84, 83, 81, 78, 74, 69, 64, 61],
  },
  commercial: {
    label: "Commercial",
    headline: "The customer can walk if deployment slips beyond 36 hours.",
    points: [
      "On-time commissioning bonus is worth 640M CR.",
      "Vantage Orbital holds the next viable launch window.",
      "Only one qualified reserve pump remains in the qualified supply network.",
    ],
    signal: [42, 44, 49, 53, 58, 64, 72, 81, 88],
  },
} as const;

export type EvidenceId = keyof typeof evidence;

export function createInitialProgramState(): ProgramState {
  return {
    phase: "readiness",
    contingencyRemaining: 286_000_000,
    committedSpend: 4_184_000_000,
    missionRisk: 34,
    confidence: 68,
    windowSecondsRemaining: 522 * 60,
    rivalDelayHours: 0,
    decisionResolved: null,
    readiness: [
      {
        id: "vehicle",
        label: "Flight vehicle",
        owner: "Arcadia Integration",
        value: 91,
        status: "watch",
        note: "Stage 2 release blocked by propulsion disposition",
      },
      {
        id: "propulsion",
        label: "Propulsion",
        owner: "Noor / Chief Engineer",
        value: 67,
        status: "hold",
        note: "Turbopump bearing anomaly QA-1044",
      },
      {
        id: "payload",
        label: "Pioneer tug",
        owner: "Customer payload team",
        value: 96,
        status: "go",
        note: "Battery top-off completes at T−04:10",
      },
      {
        id: "range",
        label: "Range & flight safety",
        owner: "Sagan Range",
        value: 100,
        status: "go",
        note: "Corridor reserved through window close",
      },
      {
        id: "ground",
        label: "Ground systems",
        owner: "Ilya / Launch operations",
        value: 88,
        status: "watch",
        note: "Propellant load sequencer on standby",
      },
      {
        id: "assurance",
        label: "Mission assurance",
        owner: "Chen / Independent review",
        value: 58,
        status: "hold",
        note: "Cannot sign unexplained rotating-machinery signature",
      },
    ],
    signoffs: [
      { id: "flight", role: "Flight director", operator: "Mara Vance", status: "pending" },
      { id: "prop", role: "Propulsion", operator: "Dr. Noor Haddad", status: "hold" },
      { id: "assurance", role: "Mission assurance", operator: "Elena Chen", status: "hold" },
      { id: "payload", role: "Payload", operator: "Ari Okafor", status: "go" },
      { id: "range", role: "Range", operator: "Sagan Control", status: "go" },
    ],
    timeline: [
      {
        id: "event-1044",
        time: "T−08:42",
        title: "QA-1044 elevated to program hold",
        body: "Mission assurance rejected the supplier's initial disposition.",
        tone: "critical",
      },
      {
        id: "event-window",
        time: "T−09:08",
        title: "Launch corridor opened",
        body: "Sagan Range confirmed an 8h 42m commitment window.",
        tone: "positive",
      },
      {
        id: "event-rival",
        time: "T−10:16",
        title: "Vantage moved to 89% readiness",
        body: "The rival program is bidding for the same customer extension.",
        tone: "warning",
      },
      {
        id: "event-stack",
        time: "T−12:31",
        title: "Integrated stack powered on",
        body: "Vehicle, payload, and ground links completed cleanly.",
        tone: "neutral",
      },
      {
        id: "event-award",
        time: "T−118d",
        title: "Kestrel Precision awarded lot 77A",
        body: "Arcadia accepted a 14% underbid and accelerated qualification to protect the build schedule.",
        tone: "warning",
      },
    ],
    outcome: null,
  };
}

export function applyDecision(state: ProgramState, choiceId: DecisionChoiceId): ProgramState {
  if (state.decisionResolved || state.outcome) return state;

  const choice = decisionChoices.find((candidate) => candidate.id === choiceId);
  if (!choice) return state;

  const readiness = state.readiness.map((item) => resolveReadiness(item, choiceId));
  const signoffs = state.signoffs.map((signoff) => resolveSignoff(signoff, choiceId));
  const statusLabel =
    choiceId === "verify"
      ? "Verification test authorized"
      : choiceId === "waive"
        ? "Flight-risk waiver recorded"
        : "Reserve assembly secured";

  return {
    ...state,
    phase: "commitment",
    contingencyRemaining: state.contingencyRemaining + choice.costDelta,
    committedSpend: state.committedSpend - choice.costDelta,
    missionRisk: clamp(state.missionRisk + choice.riskDelta, 4, 92),
    confidence: clamp(state.confidence + choice.confidenceDelta, 5, 98),
    windowSecondsRemaining: Math.max(
      0,
      state.windowSecondsRemaining + choice.windowDeltaMinutes * 60,
    ),
    rivalDelayHours: state.rivalDelayHours + choice.rivalDelayHours,
    decisionResolved: choiceId,
    readiness,
    signoffs,
    timeline: [
      {
        id: `decision-${choiceId}`,
        time: windowLabel(
          Math.max(0, state.windowSecondsRemaining / 60 + choice.windowDeltaMinutes),
        ),
        title: statusLabel,
        body: choice.consequence,
        tone: choiceId === "waive" ? "warning" : "positive",
      },
      ...state.timeline,
    ],
  };
}

export function resolveCommitment(
  state: ProgramState,
  directive: CommitmentDirective,
  roll = Math.random(),
): ProgramState {
  if (!state.decisionResolved || state.outcome) return state;

  if (directive === "scrub") {
    return {
      ...state,
      phase: "commitment",
      contingencyRemaining: state.contingencyRemaining - 76_000_000,
      committedSpend: state.committedSpend + 76_000_000,
      signoffs: state.signoffs.map((signoff) =>
        signoff.id === "flight" ? { ...signoff, status: "hold" } : signoff,
      ),
      outcome: {
        kind: "scrubbed",
        eyebrow: "Asset preserved / window lost",
        headline: "ORPHEUS-1 stands down",
        summary:
          "The vehicle returns to the integration line. Vantage inherits the first-mover window, but your flight hardware and investigation credibility survive.",
        contractDelta: -640_000_000,
        reputationDelta: -4,
        roll: null,
      },
      timeline: [
        {
          id: "commit-scrub",
          time: windowLabel(state.windowSecondsRemaining / 60),
          title: "Flight director issued SCRUB",
          body: "Range released. Customer and insurers notified.",
          tone: "warning",
        },
        ...state.timeline,
      ],
    };
  }

  const normalizedRoll = clamp(roll, 0, 1);
  const succeeded = normalizedRoll >= state.missionRisk / 100;
  const flightSignoffs = state.signoffs.map((signoff) =>
    signoff.id === "flight" ? { ...signoff, status: "go" as const } : signoff,
  );

  return {
    ...state,
    phase: "commitment",
    signoffs: flightSignoffs,
    outcome: succeeded
      ? {
          kind: "success",
          eyebrow: "Mandate delivered / network online",
          headline: "ORPHEUS-1 is on station",
          summary:
            "Pioneer Tug completed deployment and established the first autonomous cargo link to lunar orbit. Reliability becomes leverage for the next mandate.",
          contractDelta: 7_440_000_000,
          reputationDelta: 11,
          roll: normalizedRoll,
        }
      : {
          kind: "failure",
          eyebrow: "Mission incomplete / investigation open",
          headline:
            state.decisionResolved === "waive"
              ? "Stage 2 shuts down at T+06:11"
              : "Pioneer misses deployment orbit",
          summary:
            state.decisionResolved === "waive"
              ? "The bearing signature propagated under full load. The vehicle aborted safely, but your signed waiver is now the first line of the investigation."
              : "A low-probability flight fault ended the deployment. Hardware, supplier records, and every readiness sign-off are now under review.",
          contractDelta: -1_480_000_000,
          reputationDelta: -16,
          roll: normalizedRoll,
        },
    timeline: [
      {
        id: succeeded ? "commit-success" : "commit-failure",
        time: "T+00:00",
        title: succeeded ? "Mission committed successfully" : "Mission contingency declared",
        body: succeeded
          ? "Customer accepted the commissioned cargo link."
          : "Recovery, claims, and investigation protocols activated.",
        tone: succeeded ? "positive" : "critical",
      },
      ...state.timeline,
    ],
  };
}

export function advanceProgramClock(state: ProgramState, seconds = 1): ProgramState {
  if (state.outcome || state.windowSecondsRemaining <= 0 || seconds <= 0) return state;

  const windowSecondsRemaining = Math.max(0, state.windowSecondsRemaining - seconds);
  const advanced = { ...state, windowSecondsRemaining };
  return windowSecondsRemaining === 0 ? expireProgramWindow(advanced) : advanced;
}

export function choiceById(choiceId: DecisionChoiceId | null): DecisionChoice | null {
  return decisionChoices.find((choice) => choice.id === choiceId) ?? null;
}

export function windowLabel(minutes: number): string {
  const safeMinutes = Math.max(0, Math.round(minutes));
  const hours = Math.floor(safeMinutes / 60);
  const remainder = safeMinutes % 60;
  return `T−${String(hours).padStart(2, "0")}:${String(remainder).padStart(2, "0")}`;
}

function resolveReadiness(item: ReadinessItem, choiceId: DecisionChoiceId): ReadinessItem {
  if (!["vehicle", "propulsion", "ground", "assurance"].includes(item.id)) return item;

  const values: Record<
    DecisionChoiceId,
    Record<string, Pick<ReadinessItem, "value" | "status" | "note">>
  > = {
    verify: {
      vehicle: { value: 97, status: "go", note: "Stage 2 released after test review" },
      propulsion: { value: 93, status: "go", note: "Spin test showed no signature propagation" },
      ground: { value: 94, status: "go", note: "Load sequencer resumed for revised T−0" },
      assurance: { value: 92, status: "go", note: "Independent review accepted test evidence" },
    },
    waive: {
      vehicle: { value: 94, status: "watch", note: "Released by flight-director waiver" },
      propulsion: {
        value: 72,
        status: "waived",
        note: "Supplier disposition accepted without test",
      },
      ground: { value: 96, status: "go", note: "Propellant load sequence remains on plan" },
      assurance: {
        value: 64,
        status: "waived",
        note: "Objection preserved in configuration record",
      },
    },
    acquire: {
      vehicle: { value: 98, status: "go", note: "Stage 2 released after expedited swap" },
      propulsion: { value: 100, status: "go", note: "Qualified reserve installed and inspected" },
      ground: { value: 91, status: "go", note: "Countdown compressed around hardware swap" },
      assurance: { value: 98, status: "go", note: "New serialized unit accepted for flight" },
    },
  };

  return { ...item, ...values[choiceId][item.id] };
}

function resolveSignoff(signoff: Signoff, choiceId: DecisionChoiceId): Signoff {
  if (signoff.id === "prop") {
    return {
      ...signoff,
      status: choiceId === "waive" ? "conditional" : "go",
    };
  }
  if (signoff.id === "assurance") {
    return {
      ...signoff,
      status: choiceId === "waive" ? "waived" : "go",
    };
  }
  return signoff;
}

function expireProgramWindow(state: ProgramState): ProgramState {
  return {
    ...state,
    phase: "commitment",
    signoffs: state.signoffs.map((signoff) =>
      signoff.id === "flight" ? { ...signoff, status: "hold" } : signoff,
    ),
    outcome: {
      kind: "scrubbed",
      eyebrow: "Window closed / mandate exposed",
      headline: "ORPHEUS-1 misses the corridor",
      summary:
        "The readiness hold outlived the reserved range window. The vehicle is safe, but Vantage inherits first-mover position and the on-time bonus is lost.",
      contractDelta: -640_000_000,
      reputationDelta: -7,
      roll: null,
    },
    timeline: [
      {
        id: "window-expired",
        time: "T−00:00",
        title: "Primary commitment window expired",
        body: "Range released the corridor while the program remained on hold.",
        tone: "critical",
      },
      ...state.timeline,
    ],
  };
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}
