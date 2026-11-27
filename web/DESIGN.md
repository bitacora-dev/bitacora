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
`--line`, `--line-soft`, `--text`, `--muted`, `--dim`, `--label`, `--cyan`,
`--gold`, `--green`, `--red`, and `--shadow`, plus the two motion tokens
`--meter-duration` and `--meter-ease`. Treat these as the current minimum
token set, not as a finished design system.

`--dim` is **decoration only**. It measures 3.73:1 against `--panel`, below
the 4.5:1 this dashboard's small text needs, so it may colour a status dot, a
border or a disabled track and nothing a reader has to read. `--label`
(`#7d8fa6`, 5.6:1 on `--panel` and 5.2:1 on `--panel-strong`) is the readable
small-text colour and carries every uppercase label, timestamp, context line
and definition term. A new small-text colour has to be measured against
`--panel` before it is added, not chosen by eye.

Use those variables for global dashboard surfaces, text, borders, status
emphasis, and errors. Do not add new hard-coded colors in page-level CSS unless
the value is promoted into this token set and the rule explains its role.

Current exceptions are transitional and should not spread:

| Exception | Evidence | Rule |
|---|---|---|
| Chart series colors | [`App.tsx`](src/App.tsx) passes `#38bdf8` for CPU and network receive, `#f8d66d` for memory, and `#4ade80` for network transmit. | Keep CPU and receive cyan, memory gold, and transmit green; move them behind named tokens when the chart API accepts tokens directly. |
| Public surface chart color | [`PublicSurfacePanel.tsx`](src/components/PublicSurfacePanel.tsx) passes `#f8d66d` for the failed-SSH-login rate. | Security pressure is operational emphasis, not an error: reuse gold rather than red, so a permanently visible line does not read as a permanent alarm. |
| uPlot axis/grid colors | [`TimeSeriesChart.tsx`](src/components/TimeSeriesChart.tsx) sets slate axis/grid colors. | Keep chart chrome muted and lower priority than the active series. |
| Severity text colors | [`EventsList.tsx`](src/components/EventsList.tsx) uses Tailwind severity classes. | Severity color may stay semantic, but every class must measure at least 4.5:1 on `--panel`. `debug` is `text-neutral-400` (7.4:1); `text-neutral-500` measured 3.93:1 and is not allowed for severity text. |
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
| Brand/page title | `.auth-panel h1` uses large, heavy text; `.host-bar h1` is deliberately small. | Reserve the large scale for the auth shells. The dashboard's brand is a label on a working bar, not a banner. |
| Host constants | `.band-cell-value` is the largest type on the dashboard. | The five host-wide numbers are the first scan target, so they outrank every panel heading and chart readout. |
| Primary operating numbers | `.chart-value strong` uses larger text and tabular numerals. | Latest or inspected chart values are the primary scan targets inside a panel. |
| Panel titles | `.chart-head h2`, `.panel-title-row h2`, `.signal-panel h2` are compact and bold. | Panel headings label the signal; they should not compete with current values. |
| Context text | Muted labels, timestamps, availability, and window text use small muted type. | Context explains the value after the user has already seen the state. |
| Prose | Event empty states and signal coverage prose cap line length. | Text blocks must keep readable measures; wide screens are not permission to stretch paragraphs. |

Use `font-variant-numeric: tabular-nums` for changing numeric readouts, as in
`band-cell-value`, `chart-value`, host-bar metadata, and count badges. This keeps
polling updates from visually jumping.

## Grid And Responsive Behavior

The dashboard uses the full viewport width, but it does not stretch every piece
of content equally. More horizontal room should increase chart and grid reading
space while text-heavy blocks keep readable line lengths.

The overview is three stacked structures: a one-line **host bar**, a **band of
constants**, and **three columns** of panels. On a 1440×900 desktop with data
present, all three are visible at once without scrolling: the bar, the whole
band, and the first rows of every column. That is the layout's acceptance
condition, not an aspiration — a change that pushes the third column below the
fold at 1440×900 has broken it.

| Viewport | Behaviour | Rule |
|---|---|---|
| `>= 1181px` | Three columns: system, services, storage-and-operations. | The default. Each column is a flex column; `align-items: start` sits on the grid so a short column is not stretched to the tallest one's height. |
| `761px`–`1180px` | Two columns; the third spans the full width underneath. | Storage and operations is the column that moves, because disk rows and job rows are the widest content on the page. The band keeps five cells with a smaller context line rather than wrapping to four lines. |
| `<= 760px` | One column. Band at two cells, containers at two, cores at one. | Secondary table columns (the SMART verdict, the job trigger, the job duration, the tunnel peer) are hidden, and every interactive control is at least `44px` tall. ADR-0014 makes the phone the primary client, so this is a floor, not a preference. |
| `< 560px` | The disks and tunnels tables stack into labelled cards. | A table with no header row has to name each cell: `data-label` carries the heading into the stacked cell. |
| `>= 1920px` | Wider plots, a four-column container grid, larger band numbers. | Widen plots and data grids; do not turn prose into long horizontal ribbons. |
| Tall portrait, `900px+` wide and `1500px+` high in portrait orientation (including `1080×1920`) | Columns stack, the band keeps five cells, charts use `clamp(16rem, 18vh, 20rem)`. | Preserve long-form signal reading without forcing narrow landscape charts. |

There is no horizontal scrollbar at any width down to `360px`. This is
verifiable rather than a matter of opinion: `document.documentElement.scrollWidth`
must equal `clientWidth`.

Use stable dimensions for fixed-format UI: panel radius is `8px`, panel borders
are `1px`, and normal dashboard gaps are `1rem`. Chart heights now depend on
where the chart sits: `150px` inside an overview column (the shape is what is
being read there; the current value lives in the React readout beside it),
`220px` for the full-width window-detail charts below the columns, and the
tall-portrait clamp above. Changing these values is a design change, not
incidental cleanup.

## Density And Information Order

The dashboard's question is "is my server OK?" The answer order is:

1. Is the server about to lose power?
2. Can the page read this hub and host?
3. What are the host-wide constants doing right now?
4. Per subsystem: hardware, services, storage and operations.
5. What changed over the current window, in detail?
6. Which signal areas are connected or pending?

Steps 1 to 3 are the host bar and the band of constants, and they are read
top to bottom. Step 4 is read **per column**, not per page: each column is its
own top-to-bottom answer, so three subsystems are being scanned at once
instead of one after another. This is the change the dense layout makes, and
it is why the processor no longer takes a full-width row.

### Host bar

One line (wrapping on narrow screens) carrying: brand, the host's readable
name, the host selector when more than one host is enrolled, a freshness
indicator, the window label, the UPS chip, the pending-update count, the agent
version, the ULID with its copy control, and the links out of this view.

- The freshness dot **beats once per arriving sample** rather than looping.
  The element is keyed on the sample instant, so React remounts it and its
  one-shot animation runs again. A looping pulse says "this page is open"; a
  beat says "a sample landed", which is the fact being claimed. Past six
  collector cycles the pair takes the gold stale treatment and the dot stops
  moving, the same rule the processor panel's refresh line follows.
- The freshness line names **what** is being aged: `hostBarSampleAge` reads
  "muestra de hace N s" / "last sample N s ago". A bare "Actualizado" ages
  something the reader has to guess at, and the thing being claimed here is the
  sample, not the page.
- With no sample ever received the dot takes `.host-bar-pulse--idle`: it keeps
  the line's shape in a neutral fill and does not beat. Motion that fires on
  mount would assert a sample that never landed.
- The agent's own `last_seen_at` only earns a chip once it is late. While the
  host reports normally the sample age beside it already answers "is this page
  hearing from the server", and printing both spent a third of the bar saying
  one thing twice. When they diverge the chip appears and says so in words.
- The pending-update count is absent when the inventory is: a host whose
  `package_update` collector has never reported gets no chip, not a zero.
- The window label is absent until the first summary arrives. `windowMinutes`
  is `number | null`, never coerced to `0`: the averaging window is the hub's
  fact, and "Ventana de 0 min" is a window nothing reported. This is the
  absent-is-not-zero rule applied to the one bar that is always on screen.

### Band of constants

Five cells — processor, memory, network in, network out, storage — in **one
container divided by lines**, not five cards. Cards put a border, a radius and
a shadow around each number, which is three pieces of chrome per fact and
pushed the three columns below the fold at 1440×900.

Each cell carries a small label, a large tabular value, a context line, and
the window series as a trace behind it. The trace is an inline SVG
([`Sparkline`](src/components/Sparkline.tsx)), not a uPlot canvas: five of
them sit on the first screen, none has a cursor, axes or readout, and the
cell's own number is the readable current value. It is `aria-hidden` and never
the only place a value appears.

Bounded signals (ratios) pass their ceiling so an idle host does not have
one percent of noise stretched to full height. Unbounded ones (byte rates)
scale to their own peak. A cell with no samples says so in words and draws no
trace at all.

### Columns

- **System** — processor, memory, network, remote access, motherboard. The
  host's own hardware.
- **Services** — containers, pending updates, shares, public surface. What the
  host is running and how it meets the internet.
- **Storage and operations** — jobs, disks, events. What it stores and what
  has happened.

Remote access sits with the host's hardware for the reason it always has: both
describe how this host meets the outside world right now, and "can I still get
in" is asked precisely when the rest of the page is bad news.

A container's CPU reading is stated in **cores used**, not as a percentage.
The hub reports `cpu_cores_used`; a percentage needs a denominator it never
sends, and the two plausible ones disagree by the host's thread count — `0.25`
cores is "25 %" of one core the way `docker stats` means it, and "1 %" of a
24-thread host. Printing either would state a number the hub did not measure,
so the measured unit stays and the meter scales to the busiest container
rather than to an assumed ceiling.

The two inspectable window charts (CPU and memory) sit below the columns, full
width, under `window-detail`. The band's traces and the processor's history
strip say *whether* something moved; these still say exactly *when*, and they
are the only cursor on the dashboard. Below the fold is the right place for
them — not gone.

Pending collector coverage is last on the page. It describes what Bitácora
will one day show, which is never the answer to "is my server OK right now".

### Power

Power keeps one fixed place and changes intensity there.
[`PowerChip`](src/components/PowerPanel.tsx) carries the quiet state in the
host bar on every screen, including the event and log views;
[`PowerPanel`](src/components/PowerPanel.tsx) escalates into the slot directly
below the bar when a unit reports battery, and renders nothing otherwise.

The chip is what makes that split honest: a panel that exists only during the
emergency teaches the operator nothing about where to look. The quiet state is
never hidden, it is one line instead of a full panel above a dense overview.
Both render nothing at all on a host with no UPS.

### Rows that open

Events and jobs are **disclosure rows**: the whole row is the `<details>`
summary, and opening it reveals the event's source and attributes or the job's
statistics, plus the control that opens the log viewer on its `log_refs`. The
operator opens what they were already reading instead of hunting for a
separate control underneath it. The day headings stay above each run of rows.

The overview caps both lists (`OVERVIEW_EVENT_ROWS`, `OVERVIEW_JOB_ROWS`) so
one noisy hour cannot push the other columns off the screen, and the events
panel carries a "see all" control into the history view. The cap never applies
to a running job: a run in progress is the row the operator came for.

A job in progress is pinned above the history with its own treatment. Its
progress bar is drawn **only** when the extractor reported both a done and a
total figure (`overview.jobProgressRatio`). ADR-0010 lets `stats` carry
free-form keys beside the canonical ones, so a tool that reports a total is
honoured; most report neither, and a bar derived from "files transferred so
far" with no total would be a guess dressed as a measurement.
`files_checked` is deliberately not read as a total: it counts work already
done.

The pinned running card carries the same provenance a finished row does — the
trigger, and `next_expected` / `signal` / `peer_host_id` through the shared
`JobContext` — because it is the row the operator came for: a replication in
progress that does not name its peer host answers the wrong question. Finished
rows repeat their trigger **inside** the disclosure as well as in the summary,
because the summary's `.job-row-trigger` is hidden below `760px`; a field the
hub sends every ten seconds must not become unreachable on the primary client.

### Updates

[`PackageUpdatePanel`](src/components/PackageUpdatePanel.tsx) renders one row
per package, image or plugin with its installed and candidate versions, and
**one** control that applies the whole pending plan.

There is deliberately no per-package button. ADR-0022 authorises exactly two
named operations and states that the order "no acepta argumentos, rutas,
nombres de paquetes, cadenas de comando ni datos de inventario": a package name
has no path from this screen to the agent. A per-row button could only ever
apply everything while pretending to be specific, which is worse than none, so
the rows inform, the single control acts, and a note on the panel says which is
which. Changing this needs a new ADR and a protocol change, not a CSS one.

Events and operations are counted, not ranged. The metric charts are defined
by their window and keep `t.windowLabel`; the events and operations panels ask
the hub for the newest N rows with no lower time bound, because a panel that
goes blank whenever the host has been quiet for fifteen minutes says "nothing
is known", not "nothing happened". `window_secs` still means exactly what it
meant, and still only governs the series.

Because those rows can now be any age, every row carries its date as well as
its clock time — but not on the row. Rows are grouped into consecutive runs of
one calendar day and the date is stated once, in an `.event-day-heading` above
the run: "Today", "Yesterday", or weekday/day/month, with the year only when it
is not the current one. Repeating `29/9/2026` on five consecutive lines is
noise the eye has to step over on each one; omitting it entirely makes an event
from last March indistinguishable from one a minute old. The row's own `<time>`
still carries the full instant in `title` and in its accessible name, so the
unabbreviated answer is one hover or one screen reader away — which is the
failure this rule exists for: two panels reading `10:14:49` and `14:14:50`
looked adjacent and were four hours apart.

Shares, share sizes, and accounts are one subject, not three. `share`,
`share_usage`, and `user` join into a single row per share in
[`SharesPanel`](src/components/SharesPanel.tsx). Join them on the inventory
item `id`, never on `name`: `share_usage` identifies an NFS export by its full
path while `share` names it by the basename, so a name join drops every NFS
size without an error.

A share row reads across only when **the panel** is wide enough, through
`@container shares (width >= 34rem)`, not when the window is. The old
`@media (width >= 980px)` breakpoint was written when shares spanned the whole
page; in the three-column overview the same panel is about `430px` wide at a
`1440px` viewport, so a viewport-keyed query laid four columns across a third
of the page. Any panel whose internal layout depends on its own width belongs
in a container query — the overview changed every panel's width without
changing the viewport's.

Account names and their per-share permissions stay behind a `<details>`
disclosure. They are system usernames and declared privileges, and the screen
this panel lives on is shared far more often than the server is. This is a
default, not a secret: the disclosure is one click and its own copy says what
is inside. Revisit the default when ADR-0023's per-server scope lands, not by
loosening it silently.

Panels should be dense enough for repeated operations. Avoid decorative cards,
oversized empty spacing, and hero-style composition inside the app shell. The
host bar carries the window and the sample age as discrete metadata; do not
restore a separate summary-card row for those facts.

The selected host's readable name is always visible in the host bar. Resolve it
as `name`, then `hostname`, then the stable ULID. The ULID remains visible as
copyable metadata even when only one host is enrolled, because its identity is
not interchangeable with a mutable hostname (ADR-0004).

## Motion

Motion on this dashboard interpolates between two measured samples. It never
invents a value, never raises a polling cadence, and never implies activity
the page cannot observe.

- Every meter — the band is static, but the CPU bars, the memory and swap
  bars, the container meters, the disk usage bars and the job progress bar —
  animates its width over `--meter-duration` (900 ms) with `--meter-ease`, an
  exponential ease-out. The duration sits under the ten-second collector
  cadence on purpose: a bar must finish before the next real value arrives, or
  it is animating towards a number that is already stale.
  `CPU_METER_TRANSITION_MS` is kept in step with the token, because two bars
  side by side settling at different speeds reads as one of them lagging.
- The freshness dot beats once per arriving sample, as described above.
- A newly arrived event row fades in from a tinted background to the ordinary
  one. It is marked by comparing ids against what the list drew last render
  ([`useArrivedIDs`](src/components/EventsList.tsx)), and **nothing is
  highlighted on the first paint**: every row would qualify, and a page that
  flashes its whole contents on load has said nothing. Only the live overview
  passes `highlightNew`; in the history view the whole set changes on a page
  turn, and twenty rows flashing would mean "you paged".
- `prefers-reduced-motion: reduce` removes all of it: transitions, the beat,
  and the arrival highlight.

## Mirrored Charts

The network chart plots receive upwards and transmit downwards against **one
shared scale** (`TimeSeriesChart`'s `mirror` prop). Scaling each half to its
own peak would make a trickle of uploads look like a flood whenever the
download was idle, which is the exact comparison this shape exists to make.

The sign is a drawing decision only: `mirroredRange` builds a symmetric range
from the data, axis labels are formatted through `Math.abs`, and
`describePoint` still receives the measured value, so the readout never shows
a negative byte rate.

The hub reports host-wide network totals and no per-interface series, so there
is no per-interface table. Adding one is a collector and API change, not a UI
change.

## Correlation

A single correlated timeline is the product's claim, so a field that carries
correlation must be reachable, not merely received. Events and jobs carry
`log_refs`: the durable block and lines they came from. Rules:

- A row whose payload names its log lines exposes a control that opens the log
  viewer on that block, through the `?block=` filter and the page holding the
  first referenced line. Do not make the operator retype a time range.
- Referenced rows in the log viewer are marked with a visible label, not colour
  alone, and the active block filter stays visible with a control that clears
  it. A filter the operator cannot see is a filter they cannot undo.
- When the referenced lines are not on the current page, say so. Silence there
  reads as "there were no logs", which is a different and wrong answer.

The same rule applies to any field the hub already sends: declare it in
[`api.ts`](src/api.ts) even before it is drawn, because an undeclared field is
dropped with no compile error, then either render it or stop requesting it.
Downloading a value every ten seconds and discarding it is a defect, not a
neutral omission.

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
- The band of constants says an unreported constant in words and draws no
  trace for it. A `0 %` processor cell would assert an idle machine the hub
  never measured, and a `0 B/s` network cell would assert silence on a link
  nobody sampled.
- A disk with no SMART verdict is counted apart from a passing one
  (`storageSnapshot.smartUnreported`). Folding the two together would make a
  host without `bitacora-smart` look fully audited.
- `PublicSurfacePanel` has a `compact` mode for the services column: the full
  inspectable rate chart is replaced by the current rate plus its window
  trace. The question it answers ("is the rate rising right now") survives; a
  220 px canvas in a third of the page does not. Absence is still said in
  words there.
- `PublicSurfacePanel` renders "not reported" in words when a public-surface
  signal has no samples, and replaces the whole section with one explanatory
  panel when the host has reported none of them. Never substitute `0` for an
  absent security signal: a zero in "failed SSH logins" asserts that nobody
  is knocking, which is a measurement the hub does not have. Absent optional
  data and a measured zero are different answers, and this is the direction
  where being wrong is dangerous.
- Disabled controls must keep visible labels and use the existing disabled
  treatment: `cursor: not-allowed` plus reduced opacity on real buttons.

- Optional inventory whose whole subject may not exist on a host renders
  nothing rather than an empty panel. `PowerPanel`, `AccessTunnelsPanel`, and
  `SharesPanel` return `null` when their inventory has no items: a server with
  no UPS must not grow a UPS panel reading `0 %`, and a server with no shares
  must not grow an empty shares panel. This is the opposite call from
  `InventoryPanel`, whose subjects (disks, packages) exist on every host, so an
  empty list there is news.
- A panel whose subject always exists still draws **no count badge** until its
  inventory arrives. `DiskArrayPanel` and `PackageUpdatePanel` render the
  `.inventory-count` chip only when `inventory` is non-null: a `0` beside "this
  inventory has not been reported yet" reads as "no pending updates" and "no
  disks" — facts the hub never sent — and the badge is what an operator scans,
  not the sentence under it. Once the agent reports, an empty inventory does
  show `0`, because that zero is a measurement.
- A reading the host did not take under the name it is filed under must carry
  the qualifier wherever it appears, not only where it was resolved.
  `cpuTemperature` falls back to the hottest core when no `package*` sensor
  exists, so `ConstantsBand` is handed the whole `CPUTemperatureReading` rather
  than its number and prints `bandTemperatureHottestCore` when `package` is
  false. The band sits directly above the processor panel, which tags the same
  figure; a bare "60 °C" there would both contradict the panel and assert a
  package measurement that does not exist.
- `Number("")` is `0`, so every inventory attribute must be rejected as absent
  before it is parsed. Inventory attrs are strings that the collectors omit
  when they have no value, and a size, a charge, or a runtime that silently
  becomes zero is the same false answer as a zero in "failed SSH logins".
- A figure computed on a slow cadence carries when it was computed.
  `share_usage` is recalculated every 24 hours, so its size always appears with
  `calculated_at` rendered as a relative age. Presenting yesterday's number as
  current is the same defect as hiding the package cache's age.
- An inventory collected on a slow cadence states its own age the same way.
  `PackageUpdatePanel` renders `reported_at` as a relative age ("collected 4
  hours ago"), not as a bare clock time, and keeps the exact instant in the
  `<time>` element's `title` and accessible name. A timestamp is literal and
  still silent: an operator read a four-hour-old package list as current
  because nothing on screen did the subtraction for them. When the age passes
  the collector's own cadence with margin, the line takes the same gold
  `--stale` treatment `cache-age--stale` uses, paired with a sentence so the
  state never rests on colour alone.
- A boolean attribute has three states in the browser: `"true"`, `"false"`, and
  absent. Never collapse absent into `false`. "The UPS did not report its power
  source" and "the UPS is on mains" lead to opposite decisions.

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

## Processor Panel

`CPUCorePanel` answers "what are the cores doing right now" at a glance, in the
shape of a hardware dashboard rather than a catalogue. Its contract:

- One compact row per physical core, about 19 px tall, one thin bar plus its
  percentage per logical CPU inside that row. The earlier card-per-core grid
  spent a bordered box on every core, so a sixteen-core host could not be read
  without scrolling, and the panel defaulted to collapsed to hide that cost.
- The rows are a **two-column grid**, which is what lets the panel live in a
  third of the page instead of a full-width row. `coreColumnLayout` picks how
  the columns are filled: `hybrid` puts P-cores left and E-cores right when
  the topology reports both kinds, `flow` lets the rows run left to right in
  CPU order when every core is the same kind. On an Intel hybrid part the two
  kinds have different ceilings, and interleaving them put a saturated E-core
  beside an idle P-core where the pair read as one inconsistent measurement.
  A core whose kind the agent never reported is not guessed into either side;
  it stays in the left column. Below `760px` the grid collapses to one column,
  because two columns of thread bars on a phone leaves each bar about forty
  pixels wide.
- Every row carries the same background rather than alternating. With two
  columns and dense auto-placement, `:nth-child()` striping runs diagonally
  across the grid instead of down each column.
- The isolated-CPU tag wraps under its own bar instead of taking a reserved
  side column. The column was affordable at full width; inside a third of the
  page each thread row has about a hundred pixels for an id, a bar and a
  percentage, and reserving eighty of them for a state most hosts never reach
  pushed the percentage out over the tag.
- The row list never scrolls inside the panel. A clipped half-row reads as a
  rendering fault, and seeing every core at once is the panel's whole purpose.
- The header carries total load with its own wide bar, plus RAPL power and the
  CPU temperature when the host reports them. A host that exposes no package
  sensor shows its hottest core sensor and says so; it never passes a core
  reading off as the package.
- The averaging window selector governs the bars and the readouts. It is a
  trailing window anchored on the newest sample, not an epoch bucket: bucketing
  made a selected "30 s average" collapse to one sample at every boundary, so
  the bar jumped and the label was wrong while it did.
- Bars animate with a CSS width transition between two measured samples. The
  cpu collector runs every ten seconds and the dashboard polls on the same
  cadence, so movement is interpolation between real values and never invented
  data. Raising either cadence to animate would spend the agent's ADR-0024
  budget on a visual effect. `prefers-reduced-motion` removes the transition
  and the refresh pulse.
- A refresh line states the age of the newest sample and pairs it with a dot.
  Past six collector cycles the pair takes the gold stale treatment and the dot
  stops animating: an animated dot over a silent agent asserts freshness the
  page cannot know.
- Offline and isolated CPUs are tracked per thread, not per core. A core whose
  second hyperthread was taken offline still runs on the first one. An offline
  thread is dimmed, labelled in words, and keeps its empty track; it is never
  dropped from the list and never drawn as 0 %. A core whose threads are all
  offline stays in the list even after its last samples age out of retention
  (the topology still names it), dimmed as a whole row. Rows are labelled by
  their lowest CPU number and ordered by it, never by core id, which the agent
  may have reconstructed for an offline core.
- Only the refresh line re-renders every second (its own component); the rows,
  meters and formatters update with the data, not with the clock.
- The history strip under the rows is a compact uPlot chart with no axes and no
  cursor. Its readable current value lives in its own header, and the full
  inspectable CPU history stays in the `TimeSeriesChart` under the columns.

## Accessibility

The current baseline comes from [`index.css`](src/index.css),
[`App.tsx`](src/App.tsx), and the component contracts:

- Preserve visible keyboard focus with `:focus-visible`; do not remove outlines
  without replacing them with an equally visible focus treatment.
- Icon-only buttons need an accessible label. The close buttons use
  `aria-label={t.closeAria}`.
- The host-ID copy control has visible dictionary-backed text and announces its
  success or failure through a polite live region.
- Images need meaningful alt text. The pairing QR uses `alt={t.qrAlt}`.
- Text and borders must keep contrast on `--bg`, `--panel`, and
  `--panel-strong`.
- Interactive controls need stable hit areas. Existing primary buttons are at
  least `3rem` tall; icon buttons are `2rem` square on a pointer.
  Below `760px` every interactive control — link buttons, selects, inputs,
  disclosure summaries **and icon buttons** — is at least `44px` in both axes.
  There is no exemption: ADR-0014 makes the phone the primary client, and a
  `2rem` icon button is a `32px` target under a thumb. Only the hit area grows;
  the drawn glyph keeps its size.
- Close controls draw their X from `.icon-button`'s two rotated pseudo-elements.
  No interactive control carries a unicode glyph as its icon. Disclosures inside
  a panel get that `44px` height from padding rather than from `display: flex`,
  so the browser keeps drawing their native marker.
- A whole-row disclosure needs its own visible focus ring. `.event-row > details
  > summary` uses an inset `:focus-visible` outline so the ring is not clipped
  by the row's own border.
- A whole-row disclosure must also **say that it opens**. `.event-row > details >
  summary` is a `display: flex` row, so its native marker would sit before five
  flex items and break the column alignment; it is suppressed and the row draws
  its own chevron in `::after` from two rotated borders, rotating on `[open]`.
  Dropping the marker without replacing it is a defect, not a simplification:
  `cursor` and `:hover` do not exist on touch, and ADR-0014 makes the phone the
  primary client, so the log-viewer link behind the row would be undiscoverable.
- Do not rely on color alone for important state. Pair severity color with text,
  counts, labels, or panel copy.

## Review Checklist

- [ ] The change follows ADR-0013: UI code and docs are English; ADRs remain Spanish.
- [ ] New colors are either existing tokens or explicitly added to the minimum token set above.
- [ ] Phone, laptop, and `1920px+` layouts keep the same information order and readable text measures.
- [ ] At 1440×900 with data, the host bar, the whole band and the first rows of all three columns are visible without scrolling.
- [ ] `scrollWidth === clientWidth` at 1440, 1024, 390 and 360 px: no horizontal scrollbar anywhere.
- [ ] Below `760px` there is one column, the band has two cells, and no interactive control is under 44 px tall.
- [ ] Small text uses `--label` or lighter; `--dim` colours no text a reader has to read.
- [ ] New motion interpolates between two measured samples, respects `prefers-reduced-motion`, and raises no polling cadence (ADR-0024).
- [ ] No control sends a package name, a path or any inventory value to the agent (ADR-0022).
- [ ] Tall portrait (`1080×1920`) layouts stack charts and use available height.
- [ ] Empty, disabled, and pending-collector states are explicit and not treated as broken UI.
- [ ] Fields the hub already sends are declared in `api.ts` and either rendered or no longer requested.
- [ ] Inventory kinds the agent emits are either requested and rendered, or deliberately and visibly out of scope.
- [ ] Absent attributes are not parsed into zeros, and absent booleans are not read as `false`.
- [ ] Optional inventory whose subject may not exist on a host renders nothing rather than an empty panel.
- [ ] Figures computed on a slow cadence are shown with the age of the calculation.
- [ ] A timestamp that can be older than the current window states its date, once per day group rather than once per row.
- [ ] An inventory collected on a slow cadence shows its relative age, and looks different once that age passes the cadence.
- [ ] Every user-facing string, including library-driven labels, comes from `web/src/i18n/`.
- [ ] uPlot charts hide the native legend and expose a dictionary-backed current/inspected readout.
- [ ] Focus, contrast, labels, alt text, and hit areas remain keyboard and touch usable.
