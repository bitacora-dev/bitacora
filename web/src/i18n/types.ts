import type { BitacoraEvent } from "../api";

type Severity = BitacoraEvent["severity"];

export interface Dictionary {
  brand: string;
  pairingDevice: string;
  notPaired: string;
  pairButton: string;
  pairButtonPending: string;
  hostIdLabel: string;
  hostIdPlaceholder: string;
  viewButton: string;
  addDeviceButton: string;
  pairNewDeviceHeading: string;
  closeAria: string;
  generatingCode: string;
  qrAlt: string;
  expiresAt: (time: string) => string;
  hubUnreachable: (error: string) => string;
  cpuUsageTitle: string;
  memoryUsedTitle: string;
  eventsHeading: (minutes: number) => string;
  loading: string;
  noEvents: string;
  severity: Record<Severity, string>;
}
