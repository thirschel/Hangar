// The per-turn work mode the agent runs in (Copilot CLI parity): Interactive,
// Plan, or Autopilot. Kept in its own module (not Composer.tsx) so a component
// file only exports components, and both Composer and ChatViewHost can share the
// type + helpers. Named "WorkMode" to avoid clashing with the AgentMode view
// component (which is a different concept — agent vs terminal surface).

/** The per-turn work mode the agent runs in: white / blue / purple in the UI. */
export type WorkMode = 'interactive' | 'plan' | 'autopilot';

const MODE_ORDER: WorkMode[] = ['interactive', 'plan', 'autopilot'];

/** Display labels for the mode pill. */
export const MODE_LABEL: Record<WorkMode, string> = {
  interactive: 'Interactive',
  plan: 'Plan',
  autopilot: 'Autopilot',
};

/** Next mode in the Shift+Tab cycle: Interactive -> Plan -> Autopilot -> Interactive. */
export function nextWorkMode(mode: WorkMode): WorkMode {
  return MODE_ORDER[(MODE_ORDER.indexOf(mode) + 1) % MODE_ORDER.length];
}
