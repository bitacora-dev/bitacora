# Web UI Design Rules

This is the canonical interface guide for Bitácora's web dashboard. It is a
project documentation file, not an ADR, so it is written in English under
[ADR-0013](../docs/adr/0013-nombre-licencia-y-gobernanza.md). The rules below
are derived from the dashboard redesign merged in PR #43 / task #700 and from
the current `web/src/` implementation.

## Scope

These rules apply to the React/Vite dashboard under `web/src/`. The root
[`DESIGN.md`](../DESIGN.md) records the design direction; this file is the
reviewable implementation contract for future UI changes.

The interface is an Operate surface: it answers "is my server OK?" before it
explains secondary detail. It must stay read-only, calm, dense, and scan-first.

## Tokens And Color

The redesign introduced a small CSS variable set in
[`web/src/index.css`](src/index.css): `--bg`, `--panel`, `--panel-strong`,
`--line`, `--line-soft`, `--text`, `--muted`, `--dim`, `--cyan`, `--gold`,
`--green`, `--red`, and `--shadow`. Treat these as the current minimum token
set, not as a finished design system.

Use those variables for global dashboard surfaces, text, borders, status
emphasis, and errors. Do not add new hard-coded colors in page-level CSS unless
the value is promoted into this token set and the rule explains its role.

Current exceptions are transitional and should not spread:

| Exception | Evidence | Rule |
|---|---|---|
| Chart series colors | [`App.tsx`](src/App.tsx) passes `#38bdf8` for CPU and `#f8d66d` for memory. | Keep CPU cyan and memory gold; move them behind named tokens when the chart API accepts tokens directly. |
| uPlot axis/grid colors | [`TimeSeriesChart.tsx`](src/components/TimeSeriesChart.tsx) sets slate axis/grid colors. | Keep chart chrome muted and lower priority than the active series. |
| Severity text colors | [`EventsList.tsx`](src/components/EventsList.tsx) uses Tailwind severity classes. | Severity color may stay semantic, but new event severity styling must remain readable on the dark panel background. |
| Enrollment panel Tailwind classes | [`AddServerPanel.tsx`](src/components/AddServerPanel.tsx) still uses inline Tailwind utilities. | Prefer the shared CSS panel/button vocabulary for new dashboard work; do not use this as a reason to fork the visual language. |

The base palette is low-light operational UI: near-black background,
blue-gray panels, cyan for live CPU signal, gold for memory and operational
emphasis, green only for success when needed, and muted red for errors.

## Typography And Hierarchy

The root font stack is defined in [`index.css`](src/index.css) as Inter first,
then system UI fallbacks. Do not introduce a second font family for dashboard
content.

Use these hierarchy rules:

| Role | Current evidence | Rule |
|---|---|---|
| Brand/page title | `.auth-panel h1`, `.dashboard-header h1` use large, heavy text. | Reserve this scale for Bitácora and major shells only. |
| Primary operating numbers | `.status-strip strong` and `.chart-value strong` use larger text and tabular numerals. | Current CPU, memory, and latest/inspected chart values are the primary scan targets. |
| Panel titles | `.chart-head h2`, `.panel-title-row h2`, `.signal-panel h2` are compact and bold. | Panel headings label the signal; they should not compete with current values. |
| Context text | Muted labels, timestamps, availability, and window text use small muted type. | Context explains the value after the user has already seen the state. |
| Prose | Event empty states and signal coverage prose cap line length. | Text blocks must keep readable measures; wide screens are not permission to stretch paragraphs. |

Use `font-variant-numeric: tabular-nums` for changing numeric readouts, as in
`status-strip`, `chart-value`, and count badges. This keeps polling updates from
visually jumping.

## Grid And Responsive Behavior

The dashboard uses the full viewport width, but it does not stretch every piece
of content equally. More horizontal room should increase chart and grid reading
space while text-heavy blocks keep readable line lengths.

| Viewport | Current behavior | Rule |
|---|---|---|
| Phone, below `560px` | Header, status, charts, events, and signal coverage stack; values shrink; chart readout aligns left. | One-column scanning wins. Avoid side-by-side controls that create cramped touch targets. |
| Tablet/small laptop, below `980px` | Header actions stack; metrics and lower grids become one column. | Preserve order: state strip, charts, events, signal coverage. |
| Laptop/default | Status strip has three columns; CPU and memory charts sit side by side; events and signal coverage share the lower row. | Keep the current state visible without scrolling on ordinary laptop sizes when data is present. |
| Wide desktop, `1920px+` | Charts use wider tracks, lower grid grows, and text blocks remain capped. | Widen plots and data grids; do not turn prose into long horizontal ribbons. |

Use stable dimensions for fixed-format UI: chart height is `220px`, panel radius
is `8px`, panel borders are `1px`, and normal dashboard gaps are `1rem` to
`1.25rem`. Changing these values is a design change, not incidental cleanup.

## Density And Information Order

The dashboard's question is "is my server OK?" The answer order is:

1. Can the page read this hub and host?
2. What are CPU and memory doing now?
3. What changed over the current window?
4. Were any events emitted?
5. Which signal areas are connected or pending?

This order is encoded in [`App.tsx`](src/App.tsx): auth and host selection
states first, then `status-strip`, `metrics-grid`, `EventsList`, and signal
coverage. Do not lead with setup prose, marketing copy, or secondary collector
detail on the main dashboard.

Panels should be dense enough for repeated operations. Avoid decorative cards,
oversized empty spacing, and hero-style composition inside the app shell.

## Empty And Disabled States

Empty is normal in Bitácora. Production event streams may stay empty for long
periods, and optional collectors may not report yet.

Use explicit, dignified empty states:

- `EventsList` renders `eventsEmptyHeading` and `eventsEmptyBody` when there are
  no rows. Keep this explanatory state; do not replace it with a blank panel.
- `TimeSeriesChart` shows `noSamples` in its readout when a series is empty.
  Keep the panel shape stable while data is missing.
- Signal coverage in `App.tsx` describes optional collectors as pending
  capability, not as broken UI.
- Disabled controls must keep visible labels and use the existing disabled
  treatment: `cursor: not-allowed` plus reduced opacity on real buttons.

Do not communicate unavailable collectors as errors unless the backend reports a
fault. Absence of optional data and hub/API failure are different states.

## i18n

Spanish is the default runtime locale. [`context.tsx`](src/i18n/context.tsx)
sets `DEFAULT_LOCALE` to `es` and writes `document.documentElement.lang`.
English exists alongside it through the same dictionary contract.

Rules:

- No user-facing literal strings in React components. Add keys to
  [`types.ts`](src/i18n/types.ts), [`locales/es.ts`](src/i18n/locales/es.ts),
  and [`locales/en.ts`](src/i18n/locales/en.ts).
- Keep the dictionary key set identical across locales; the Vitest coverage in
  [`dictionaries.test.ts`](src/i18n/dictionaries.test.ts) enforces this.
- Format times and numbers with `intlTag` from `useTranslation()`, as
  `App.tsx`, `EventsList.tsx`, and `TimeSeriesChart.tsx` already do.
- Audit library defaults. uPlot's native legend previously exposed generic
  English labels such as `Time`; the redesign hides that legend and renders the
  readout in React so labels come from the dictionaries.
- The brand is `Bitacora` in identifiers and `Bitácora` in user-visible brand
  copy, per ADR-0013.

## uPlot Conventions

Charts use uPlot for rendering and React for meaning. The current convention in
[`TimeSeriesChart.tsx`](src/components/TimeSeriesChart.tsx) is:

- `legend: { show: false }`; do not use uPlot's built-in legend for user-facing
  labels.
- A custom readout outside the canvas always shows the latest sample by default.
- Cursor inspection changes the readout to the inspected sample, then clears on
  mouse leave and touch end/cancel.
- `ResizeObserver` owns chart resizing. Do not hard-code viewport widths.
- The x scale is time-based; y-axis labels are formatted by the caller through
  `formatAxisValue`.
- Points are visible only for sparse series (`points.length < 60`) to avoid
  visual noise.
- Series fill should stay subtle. The active value and line carry the signal;
  background fill is secondary context.

When adding a chart, define what the current value means before choosing the
series shape. A chart without a readable current value is incomplete for this
dashboard.

## Accessibility

The current baseline comes from [`index.css`](src/index.css),
[`App.tsx`](src/App.tsx), and the component contracts:

- Preserve visible keyboard focus with `:focus-visible`; do not remove outlines
  without replacing them with an equally visible focus treatment.
- Icon-only buttons need an accessible label. The close buttons use
  `aria-label={t.closeAria}`.
- Images need meaningful alt text. The pairing QR uses `alt={t.qrAlt}`.
- The status strip has an accessible label from the dictionary.
- Text and borders must keep contrast on `--bg`, `--panel`, and
  `--panel-strong`.
- Interactive controls need stable hit areas. Existing primary buttons are at
  least `3rem` tall; icon buttons are `2rem` square and should not shrink.
- Do not rely on color alone for important state. Pair severity color with text,
  counts, labels, or panel copy.

## Review Checklist

- [ ] The change follows ADR-0013: UI code and docs are English; ADRs remain Spanish.
- [ ] New colors are either existing tokens or explicitly added to the minimum token set above.
- [ ] Phone, laptop, and `1920px+` layouts keep the same information order and readable text measures.
- [ ] Empty, disabled, and pending-collector states are explicit and not treated as broken UI.
- [ ] Every user-facing string, including library-driven labels, comes from `web/src/i18n/`.
- [ ] uPlot charts hide the native legend and expose a dictionary-backed current/inspected readout.
- [ ] Focus, contrast, labels, alt text, and hit areas remain keyboard and touch usable.
