// Validate observation responses and translate dashboard inputs into the backend contract.
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

/** Stop parsing an invalid response with the shared, user-facing validation error. */
function fail(): never {
  throw new ResponseValidationError();
}

/** Require an object before validating its observation fields. */
function obj(value: unknown) {
  if (isRecord(value)) {
    return value;
  }

  return fail();
}

/** Validate a bounded string without silently coercing another JSON type. */
function text(value: unknown, max = 2000, empty = false) {
  if (
    typeof value === "string" &&
    value.length <= max &&
    (empty || value.trim())
  ) {
    return value;
  }

  return fail();
}

/** Validate a finite numeric value within the supplied bounds. */
function num(value: unknown, min = 0, max = Number.MAX_SAFE_INTEGER) {
  if (
    typeof value === "number" &&
    Number.isFinite(value) &&
    value >= min &&
    value <= max
  ) {
    return value;
  }

  return fail();
}

/** Require an exact safe integer after validating its numeric range. */
function integer(value: unknown, min = 0, max = Number.MAX_SAFE_INTEGER) {
  const numericValue = num(value, min, max);

  if (Number.isSafeInteger(numericValue)) {
    return numericValue;
  }

  return fail();
}

/** Require a real JSON boolean rather than a truthy substitute. */
function bool(value: unknown) {
  if (typeof value === "boolean") {
    return value;
  }

  return fail();
}

/** Accept only one of the supported observation category values. */
function one<const T extends readonly string[]>(
  value: unknown,
  choices: T,
): T[number] {
  return choices.find((allowedValue) => allowedValue === value) ?? fail();
}

/** Require the UUIDv4 format used by capture and save requests. */
function id(value: unknown) {
  const result = text(value, 36);

  if (
    /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
      result,
    )
  ) {
    return result;
  }

  return fail();
}

/** Validate captured conditions and keep simulated reference quotes separate from actual fares. */
export function parseSnapshot(value: unknown): Snapshot {
  const snapshotData = obj(value);

  const tripData = obj(snapshotData.trip);

  const route = parseRoute(tripData.route);

  const distanceKm = num(tripData.distanceKm);

  const passengers = integer(tripData.passengers, 1, 6);

  const rideType = one(tripData.rideType, ["standard", "six"]);

  if (
    Math.abs(distanceKm - route.distanceMeters / 1000) > 1e-9 ||
    (passengers > 4 && rideType !== "six")
  ) {
    return fail();
  }

  if (
    !Array.isArray(snapshotData.demoQuotes) ||
    snapshotData.demoQuotes.length > 256
  ) {
    return fail();
  }

  const demoQuotes = snapshotData.demoQuotes.map(parseQuote);

  if (
    demoQuotes.some(
      (quote) =>
        quote.source !== "simulated" &&
        !(quote.source === "unknown" && quote.status === "unavailable"),
    )
  ) {
    return fail();
  }

  return {
    id: id(snapshotData.id),
    capturedAt: parseTimestamp(snapshotData.capturedAt),
    featureVersion: text(snapshotData.featureVersion, 80),
    demoVersion: text(snapshotData.demoVersion, 80),
    pickupZone: one(snapshotData.pickupZone, zones),
    dropoffZone: one(snapshotData.dropoffZone, zones),
    demoQuotes,
    trip: {
      pickup: parseLocation(tripData.pickup),
      dropoff: parseLocation(tripData.dropoff),
      distanceKm,
      passengers,
      rideType,
      scenario: one(tripData.scenario, ["normal", "busy", "rain", "outage"]),
      route,
    },
  };
}

/** Validate a manual fare and verify its quality flags against the immutable snapshot. */
export function parseObservation(value: unknown): Observation {
  const recordData = obj(value);

  const snapshot = parseSnapshot(recordData.snapshot);

  const sessionId = id(recordData.sessionId);

  const quotedAt = parseTimestamp(recordData.quotedAt);

  const createdAt = parseTimestamp(recordData.createdAt);

  const updatedAt = parseTimestamp(recordData.updatedAt);

  const priceCents = integer(recordData.priceCents, 0, 1000000);

  const includedFeesCents =
    recordData.includedFeesCents === null
      ? null
      : integer(recordData.includedFeesCents, 0, priceCents);

  const contextAligned = bool(recordData.contextAligned);

  const trainingEligible = bool(recordData.trainingEligible);

  const fareKind = one(recordData.fareKind, ["quoted", "paid"]);

  if (
    sessionId !== snapshot.id ||
    Date.parse(updatedAt) < Date.parse(createdAt) ||
    trainingEligible !==
      (fareKind === "quoted" &&
        contextAligned &&
        snapshot.trip.route.source === "onemap")
  ) {
    return fail();
  }

  const gap = Date.parse(quotedAt) - Date.parse(snapshot.capturedAt);

  if (contextAligned !== (gap >= -60000 && gap <= 300000)) {
    return fail();
  }

  const traffic = snapshot.trip.route.traffic;

  const fresh =
    !!traffic &&
    snapshot.trip.route.trafficAware &&
    !!traffic.observedAt &&
    !!traffic.expiresAt &&
    Date.parse(traffic.observedAt) <= Date.parse(quotedAt) &&
    Date.parse(quotedAt) < Date.parse(traffic.expiresAt);

  if (bool(recordData.trafficFreshAtQuote) !== fresh) {
    return fail();
  }

  return {
    id: id(recordData.id),
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
    revision: integer(recordData.revision, 1),
    provider: one(recordData.provider, providers),
    product: text(recordData.product, 80),
    weather: one(recordData.weather, ["unknown", "dry", "rain"]),
    promotion: one(recordData.promotion, ["unknown", "none", "applied"]),
    notes: text(recordData.notes, 1000, true),
    trafficFreshAtQuote: bool(recordData.trafficFreshAtQuote),
    singaporeWeekday: integer(recordData.singaporeWeekday, 1, 7),
    timeSlot: one(recordData.timeSlot, [
      "morning",
      "midday",
      "evening",
      "night",
    ]),
  };
}

/** Validate pagination, record identifiers and the collection coverage summaries. */
export function parsePage(value: unknown): Page {
  const pageData = obj(value);

  const statsData = obj(pageData.stats);

  if (!Array.isArray(pageData.records) || pageData.records.length > 100) {
    return fail();
  }

  const groups = (allowedValue: unknown) => {
    if (!Array.isArray(allowedValue) || allowedValue.length > 20) {
      return fail();
    }

    return allowedValue.map((item) => {
      const groupData = obj(item);

      return {
        label: text(groupData.label, 80),
        count: integer(groupData.count),
      };
    });
  };

  const records = pageData.records.map(parseObservation);

  const total = integer(pageData.total);

  const stats = {
    total: integer(statsData.total),
    quotes: integer(statsData.quotes),
    paid: integer(statsData.paid),
    routes: integer(statsData.routes),
    byProvider: groups(statsData.byProvider),
    byTimeSlot: groups(statsData.byTimeSlot),
    byWeekday: groups(statsData.byWeekday),
  };

  if (
    records.length > total ||
    new Set(records.map((recordData) => recordData.id)).size !==
      records.length ||
    stats.quotes + stats.paid !== stats.total ||
    stats.total < total
  ) {
    return fail();
  }

  return { records, total, stats };
}

/** Parse a decimal SGD input into exact integer cents without floating-point rounding. */
export function parsePrice(value: string): number {
  const trimmed = value.trim();

  if (!/^\d{1,5}(?:\.\d{1,2})?$/.test(trimmed)) {
    throw new Error("Enter dollars with at most two decimal places.");
  }

  const [dollars, fraction = ""] = trimmed.split(".");

  const cents = Number(dollars) * 100 + Number(fraction.padEnd(2, "0"));

  if (cents > 1000000) {
    throw new Error("The fare must be at most $10,000.");
  }

  return cents;
}

/** Format a timestamp for the dashboard input using Singapore time regardless of device timezone. */
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

  const part = (name: string) =>
    parts.find((pageData) => pageData.type === name)!.value;

  return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}:${part("second")}`;
}

/** Convert a validated Singapore calendar input into an explicit UTC timestamp. */
export function singaporeISO(value: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?$/.test(value)) {
    throw new Error("Enter the date and time you checked the fare.");
  }

  parseTimestamp(`${value.length === 16 ? value + ":00" : value}+08:00`);

  return new Date(`${value}+08:00`).toISOString();
}

/** Create a UUIDv4 for safe retries using cryptographically generated random bytes. */
export function newID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));

  bytes[6] = (bytes[6] & 15) | 64;

  bytes[8] = (bytes[8] & 63) | 128;

  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join(
    "",
  );

  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/** Rebuild a trip request from a saved snapshot, preserving custom endpoint coordinates. */
export function requestForSnapshot(snapshot: Snapshot): TripRequest {
  const tripData = snapshot.trip;

  return {
    pickupId: tripData.pickup.id,
    dropoffId: tripData.dropoff.id,
    passengers: tripData.passengers,
    rideType: tripData.rideType,
    scenario: tripData.scenario,
    ...(["map-location", "current-location"].includes(tripData.pickup.id)
      ? {
          pickupCoordinates: {
            latitude: tripData.pickup.latitude,
            longitude: tripData.pickup.longitude,
          },
        }
      : {}),
    ...(["map-location", "current-location"].includes(tripData.dropoff.id)
      ? {
          dropoffCoordinates: {
            latitude: tripData.dropoff.latitude,
            longitude: tripData.dropoff.longitude,
          },
        }
      : {}),
  };
}

/** Check whether the backend has configured observation storage. */
export function status(signal?: AbortSignal) {
  return request(
    "/api/observations/status",
    (value) => {
      const statusData = obj(value);

      if (statusData.storage !== "postgresql" || statusData.mode !== "local") {
        return fail();
      }

      return {
        enabled: bool(statusData.enabled),
        notice: text(statusData.notice),
      };
    },
    { signal },
  );
}

/** Ask Go to resolve and save the current route and traffic conditions. */
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

/** Retrieve the immutable conditions for an existing capture identifier. */
export function getSnapshot(sessionId: string, signal?: AbortSignal) {
  return request(`/api/observation-sessions/${id(sessionId)}`, parseSnapshot, {
    signal,
  });
}

/** Submit a manually observed fare with its stable save and capture identifiers. */
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

/** Correct a manual fare using its expected revision to detect concurrent edits. */
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

/** Request a filtered page of observations with its collection-wide coverage statistics. */
export function list(
  provider = "",
  searchQuery = "",
  offset = 0,
  signal?: AbortSignal,
) {
  const query = new URLSearchParams({
    provider,
    q: searchQuery,
    offset: String(offset),
    limit: "20",
  });

  return request(`/api/observations?${query}`, parsePage, { signal });
}
