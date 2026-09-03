# Design

<!-- impeccable:design-schema 1 -->

The canonical implementation rules for the web dashboard live in
[`web/DESIGN.md`](web/DESIGN.md). This root document records the design
direction produced by the dashboard redesign; use the web guide when reviewing
frontend changes.

## Surface

The main Bitacora dashboard is an Operate surface. It is designed for quick server-state inspection, not persuasion or storytelling.

## Visual World

The dashboard uses a low-light control-room language: dark operational panels, tabular numerals, thin signal lines, and restrained cyan/gold highlights. The Impeccable direction seed was `f73d7d36`; the assigned source was CRT arcade pixel glow, translated into telemetry clarity rather than game ornament.

## Layout

The page uses the full viewport width. Phone screens stack status, charts, and events into one column. Laptop screens show status across the top and metrics in parallel where space allows. Wide desktop screens keep CPU and memory charts side by side with broad horizontal plotting space, while text-heavy blocks retain readable line lengths.

## Components

Panels use 8px corners, one-pixel borders, dark layered backgrounds, and real content density. Charts hide uPlot's built-in legend and render a custom current-value readout that defaults to the latest sample and follows cursor inspection. Event lists are designed around the empty state as a normal production condition.

## Color

The base palette is near-black and blue-gray, with cyan for live CPU signal and gold for memory/operational emphasis. Error states use muted red with enough contrast against the dark surface.

## Typography

System UI sans is used for maintainability. Values rely on tabular numerals and clear scale changes; prose is constrained to readable measures and never stretches across wide monitors.

## Motion and State

Motion is limited to native chart interaction and cursor inspection. Disabled or not-yet-connected collectors are communicated as pending signal coverage rather than blank layout space.
