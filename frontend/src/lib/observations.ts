import {
  isRecord,
  parseLocation,
  parseQuote,
  parseRoute,
  parseTimestamp,
  ResponseValidationError,
} from "./contracts";
import type { Comparison, Quote } from "./contracts";
import { request } from "./api";
import type { TripRequest } from "./api";

export const zones = [
  "unknown",
  "residential",
  "office",
  "industrial",
  "retail",
  "airport",
  "mixed",
] as const;
export type Zone = (typeof zones)[number];
export const providers = ["grab", "gojek", "zig", "ryde"] as const;
export type Provider = (typeof providers)[number];
export const providerNames: Record<Provider, string> = {
  grab: "Grab",
  gojek: "Gojek",
  zig: "CDG Zig",
  ryde: "Ryde",
};
export type Snapshot = {
  id: string;
  capturedAt: string;
  featureVersion: string;
  demoVersion: string;
  trip: Comparison["trip"];
  demoQuotes: Quote[];
  pickupZone: Zone;
  dropoffZone: Zone;
};
export type Fields = {
  provider: Provider;
  product: string;
  priceCents: number;
  fareKind: "quoted" | "paid";
  quotedAt: string;
  weather: "unknown" | "dry" | "rain";
  promotion: "unknown" | "none" | "applied";
  includedFeesCents: number | null;
  notes: string;
};
export type Observation = Fields & {
  id: string;
  sessionId: string;
  revision: number;
  createdAt: string;
  updatedAt: string;
  snapshot: Snapshot;
  contextAligned: boolean;
  trafficFreshAtQuote: boolean;
  trainingEligible: boolean;
  singaporeWeekday: number;
  timeSlot: "morning" | "midday" | "evening" | "night";
};
export type Group = { label: string; count: number };
export type Page = {
  records: Observation[];
  total: number;
  stats: {
    total: number;
    quotes: number;
    paid: number;
    routes: number;
    byProvider: Group[];
    byTimeSlot: Group[];
    byWeekday: Group[];
  };
};
function fail(): never {
  throw new ResponseValidationError();
}
function obj(value: unknown) {
  return isRecord(value) ? value : fail();
}
function text(value: unknown, max = 2000, empty = false) {
  return typeof value === "string" &&
    value.length <= max &&
    (empty || value.trim())
    ? value
    : fail();
}
function num(value: unknown, min = 0, max = Number.MAX_SAFE_INTEGER) {
  return typeof value === "number" &&
    Number.isFinite(value) &&
    value >= min &&
    value <= max
    ? value
    : fail();
}
function integer(value: unknown, min = 0, max = Number.MAX_SAFE_INTEGER) {
  const n = num(value, min, max);
  return Number.isSafeInteger(n) ? n : fail();
}
function bool(value: unknown) {
  return typeof value === "boolean" ? value : fail();
}
function one<const T extends readonly string[]>(
  value: unknown,
  choices: T,
): T[number] {
  return choices.find((v) => v === value) ?? fail();
}
function id(value: unknown) {
  const result = text(value, 36);
  return /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
    result,
  )
    ? result
    : fail();
}
export function parseSnapshot(value: unknown): Snapshot {
  const s = obj(value),
    t = obj(s.trip),
    route = parseRoute(t.route);
  const distanceKm = num(t.distanceKm);
  const passengers = integer(t.passengers, 1, 6),
    rideType = one(t.rideType, ["standard", "six"]);
  if (
    Math.abs(distanceKm - route.distanceMeters / 1000) > 1e-9 ||
    (passengers > 4 && rideType !== "six")
  )
    return fail();
  if (!Array.isArray(s.demoQuotes) || s.demoQuotes.length > 256) return fail();
  const demoQuotes = s.demoQuotes.map(parseQuote);
  if (
    demoQuotes.some(
      (q) =>
        q.source !== "simulated" &&
        !(q.source === "unknown" && q.status === "unavailable"),
    )
  )
    return fail();
  return {
    id: id(s.id),
    capturedAt: parseTimestamp(s.capturedAt),
    featureVersion: text(s.featureVersion, 80),
    demoVersion: text(s.demoVersion, 80),
    pickupZone: one(s.pickupZone, zones),
    dropoffZone: one(s.dropoffZone, zones),
    demoQuotes,
    trip: {
      pickup: parseLocation(t.pickup),
      dropoff: parseLocation(t.dropoff),
      distanceKm,
      passengers,
      rideType,
      scenario: one(t.scenario, ["normal", "busy", "rain", "outage"]),
      route,
    },
  };
}
export function parseObservation(value: unknown): Observation {
  const r = obj(value),
    snapshot = parseSnapshot(r.snapshot);
  const sessionId = id(r.sessionId),
    quotedAt = parseTimestamp(r.quotedAt),
    createdAt = parseTimestamp(r.createdAt),
    updatedAt = parseTimestamp(r.updatedAt);
  const priceCents = integer(r.priceCents, 0, 1000000),
    includedFeesCents =
      r.includedFeesCents === null
        ? null
        : integer(r.includedFeesCents, 0, priceCents);
  const contextAligned = bool(r.contextAligned),
    trainingEligible = bool(r.trainingEligible),
    fareKind = one(r.fareKind, ["quoted", "paid"]);
  if (
    sessionId !== snapshot.id ||
    Date.parse(updatedAt) < Date.parse(createdAt) ||
    trainingEligible !==
      (fareKind === "quoted" &&
        contextAligned &&
        snapshot.trip.route.source === "onemap")
  )
    return fail();
  const gap = Date.parse(quotedAt) - Date.parse(snapshot.capturedAt);
  if (contextAligned !== (gap >= -60000 && gap <= 300000)) return fail();
  const traffic = snapshot.trip.route.traffic;
  const fresh =
    !!traffic &&
    snapshot.trip.route.trafficAware &&
    !!traffic.observedAt &&
    !!traffic.expiresAt &&
    Date.parse(traffic.observedAt) <= Date.parse(quotedAt) &&
    Date.parse(quotedAt) < Date.parse(traffic.expiresAt);
  if (bool(r.trafficFreshAtQuote) !== fresh) return fail();
  return {
    id: id(r.id),
    sessionId,
    snapshot,
    quotedAt,
    createdAt,
    updatedAt,
    priceCents,
    includedFeesCents,
    contextAligned,
    trainingEligible,
    fareKind,
    revision: integer(r.revision, 1),
    provider: one(r.provider, providers),
    product: text(r.product, 80),
    weather: one(r.weather, ["unknown", "dry", "rain"]),
    promotion: one(r.promotion, ["unknown", "none", "applied"]),
    notes: text(r.notes, 1000, true),
    trafficFreshAtQuote: bool(r.trafficFreshAtQuote),
    singaporeWeekday: integer(r.singaporeWeekday, 1, 7),
    timeSlot: one(r.timeSlot, ["morning", "midday", "evening", "night"]),
  };
}
export function parsePage(value: unknown): Page {
  const p = obj(value),
    s = obj(p.stats);
  if (!Array.isArray(p.records) || p.records.length > 100) return fail();
  const groups = (v: unknown) => {
    if (!Array.isArray(v) || v.length > 20) return fail();
    return v.map((item) => {
      const g = obj(item);
      return { label: text(g.label, 80), count: integer(g.count) };
    });
  };
  const records = p.records.map(parseObservation),
    total = integer(p.total),
    stats = {
      total: integer(s.total),
      quotes: integer(s.quotes),
      paid: integer(s.paid),
      routes: integer(s.routes),
      byProvider: groups(s.byProvider),
      byTimeSlot: groups(s.byTimeSlot),
      byWeekday: groups(s.byWeekday),
    };
  if (
    records.length > total ||
    new Set(records.map((r) => r.id)).size !== records.length ||
    stats.quotes + stats.paid !== stats.total ||
    stats.total < total
  )
    return fail();
  return { records, total, stats };
}
export function parsePrice(value: string): number {
  const trimmed = value.trim();
  if (!/^\d{1,5}(?:\.\d{1,2})?$/.test(trimmed))
    throw new Error("Enter dollars with at most two decimal places.");
  const [dollars, fraction = ""] = trimmed.split(".");
  const cents = Number(dollars) * 100 + Number(fraction.padEnd(2, "0"));
  if (cents > 1000000) throw new Error("The fare must be at most $10,000.");
  return cents;
}
export function singaporeInput(date = new Date()): string {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Singapore",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).formatToParts(date);
  const part = (name: string) => parts.find((p) => p.type === name)!.value;
  return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}:${part("second")}`;
}
export function singaporeISO(value: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?$/.test(value))
    throw new Error("Enter the date and time you checked the fare.");
  parseTimestamp(`${value.length === 16 ? value + ":00" : value}+08:00`);
  return new Date(`${value}+08:00`).toISOString();
}
export function newID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join(
    "",
  );
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
export function requestForSnapshot(s: Snapshot): TripRequest {
  const t = s.trip;
  return {
    pickupId: t.pickup.id,
    dropoffId: t.dropoff.id,
    passengers: t.passengers,
    rideType: t.rideType,
    scenario: t.scenario,
    ...(["map-location", "current-location"].includes(t.pickup.id)
      ? {
          pickupCoordinates: {
            latitude: t.pickup.latitude,
            longitude: t.pickup.longitude,
          },
        }
      : {}),
    ...(["map-location", "current-location"].includes(t.dropoff.id)
      ? {
          dropoffCoordinates: {
            latitude: t.dropoff.latitude,
            longitude: t.dropoff.longitude,
          },
        }
      : {}),
  };
}
export function status(signal?: AbortSignal) {
  return request(
    "/api/observations/status",
    (value) => {
      const s = obj(value);
      if (s.storage !== "postgresql" || s.mode !== "local") return fail();
      return { enabled: bool(s.enabled), notice: text(s.notice) };
    },
    { signal },
  );
}
export function capture(
  input: {
    id: string;
    trip: TripRequest;
    pickupLabel: string;
    dropoffLabel: string;
    pickupZone: Zone;
    dropoffZone: Zone;
  },
  signal?: AbortSignal,
) {
  return request("/api/observation-sessions", parseSnapshot, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal,
  });
}
export function getSnapshot(sessionId: string, signal?: AbortSignal) {
  return request(`/api/observation-sessions/${id(sessionId)}`, parseSnapshot, {
    signal,
  });
}
export function save(
  input: Fields & { id: string; sessionId: string },
  signal?: AbortSignal,
) {
  return request("/api/observations", parseObservation, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal,
  });
}
export function edit(
  observationId: string,
  input: Fields & { revision: number },
  signal?: AbortSignal,
) {
  return request(`/api/observations/${id(observationId)}`, parseObservation, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal,
  });
}
export function list(provider = "", q = "", offset = 0, signal?: AbortSignal) {
  const query = new URLSearchParams({
    provider,
    q,
    offset: String(offset),
    limit: "20",
  });
  return request(`/api/observations?${query}`, parsePage, { signal });
}
