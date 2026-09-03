# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary users are self-hosters and server administrators checking Linux hosts from a browser or PWA. Inferred from the task brief and ADR-0014: the dashboard must work from a phone, a laptop, and a wide desktop monitor.

## Product Purpose

Bitacora is self-hosted observability and diagnostics for Linux servers. It helps users understand server state and recent incidents without opening an SSH session.

## Positioning

Bitacora combines metrics, events, logs, inventory, and black-box diagnostics in a single read-only product, with no mandatory external SaaS dependency.

## Operating Context

The main dashboard is an operator surface. Users scan current CPU and memory state first, then use short time-series context and recent events to decide whether the server needs attention.

## Capabilities and Constraints

The web UI is read-only by architecture decision ADR-0012. The current summary endpoint is intentionally scoped to CPU, memory, and events; inventory, disks, network, updates, and hardware detail are outside this task.

The interface is localized in Spanish and English, with Spanish as the default. New UI strings must be present in both dictionaries.

## Brand Commitments

The product name is Bitacora. The interface should communicate precision, calm operational confidence, and self-hosted control without implying write access to the observed machine.

## Evidence on Hand

README.md describes the product purpose and status. ADR-0012 defines the read-only constraint. ADR-0014 defines the PWA/mobile client direction. ADR-0015 states the goal of checking server state without SSH and the broader observed-surface roadmap.

## Product Principles

- Make the current server state visible without interaction.
- Treat time-series charts as context for the current value, not as the only source of meaning.
- Prefer absolute operational values over abstract percentages.
- Degrade collectors and empty event streams with explicit, useful states.
- Preserve the read-only trust boundary.

## Accessibility & Inclusion

No product-specific accessibility standard is recorded yet. This interface should still maintain readable contrast, keyboard focus, and responsive layouts across phone, laptop, and wide desktop use.
