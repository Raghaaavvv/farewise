// Parse unknown JSON into the exact shapes consumed by the UI. Extra fields
// are ignored so additive API changes work; required fields are never coerced.
export type Location = {
  id: string;
  name: string;
  detail: string;
  latitude: number;
  longitude: number;
};

export type ProductType = "standard" | "premium" | "pool" | "taxi";
export type RoutingSource = "approximate" | "onemap";
export type Route = {
  source: RoutingSource;
  distanceMeters: number;
  durationSeconds: number | null;
  trafficAware: false;
};
export type FareRange = {
  minFare: number;
  maxFare: number;
  isEstimateRange: boolean;
};
export type Quote = {
  providerId: string;
  providerName: string;
  productId: string;
  productType: ProductType;
  serviceName: string;
  status: "available" | "partial" | "unavailable" | "expired";
  currency: "SGD";
  fare: FareRange | null;
  etaMinutes: number | null;
  source: "simulated" | "model" | "live" | "unknown";
  fetchedAt: string;
  expiresAt: string;
  cached: boolean;
  capacity: number;
  message: string;
  feesDescription: string;
};

export type Comparison = {
  mode: "demo";
  notice: string;
  trip: {
    pickup: Location;
    dropoff: Location;
    passengers: number;
    rideType: "standard" | "six";
    scenario: "normal" | "busy" | "rain" | "outage";
    distanceKm: number;
    route: Route;
  };
  quotes: Quote[];
  collectedAt: string;
  expiresAt: string;
};

export class ResponseValidationError extends Error {
  constructor() {
    super(
      "The comparison service returned an invalid response. Please refresh or try again.",
    );
    this.name = "ResponseValidationError";
  }
}

function fail(): never {
  throw new ResponseValidationError();
}
export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function record(value: unknown) {
  return isRecord(value) ? value : fail();
}
function text(value: unknown, allowEmpty = false): string {
  return typeof value === "string" &&
    value.length <= 2000 &&
    (allowEmpty || value.trim().length > 0)
    ? value
    : fail();
}
function number(
  value: unknown,
  min: number,
  max = Number.MAX_SAFE_INTEGER,
): number {
  return typeof value === "number" &&
    Number.isFinite(value) &&
    value >= min &&
    value <= max
    ? value
    : fail();
}
function integer(
  value: unknown,
  min: number,
  max = Number.MAX_SAFE_INTEGER,
): number {
  const result = number(value, min, max);
  return Number.isSafeInteger(result) ? result : fail();
}
function boolean(value: unknown): boolean {
  return typeof value === "boolean" ? value : fail();
}
function choice<const T extends readonly string[]>(
  value: unknown,
  values: T,
): T[number] {
  for (const allowed of values) if (allowed === value) return allowed;
  return fail();
}
function timestamp(value: unknown): string {
  const result = text(value);
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.exec(
      result,
    );
  if (!match || !Number.isFinite(Date.parse(result))) return fail();
  const [, year, month, day, hour, minute, second] = match;
  const days = new Date(Date.UTC(Number(year), Number(month), 0)).getUTCDate();
  if (
    Number(month) < 1 ||
    Number(month) > 12 ||
    Number(day) < 1 ||
    Number(day) > days ||
    Number(hour) > 23 ||
    Number(minute) > 59 ||
    Number(second) > 59
  )
    return fail();
  return result;
}
function list(value: unknown, max: number): unknown[] {
  return Array.isArray(value) && value.length <= max ? value : fail();
}

export function parseLocation(value: unknown): Location {
  const obj = record(value);
  return {
    id: text(obj.id),
    name: text(obj.name),
    detail: text(obj.detail, true),
    latitude: number(obj.latitude, -90, 90),
    longitude: number(obj.longitude, -180, 180),
  };
}

function parseFare(value: unknown): FareRange | null {
  if (value === null) return null;
  const obj = record(value);
  const minFare = integer(obj.minFare, 0),
    maxFare = integer(obj.maxFare, minFare);
  const isEstimateRange = boolean(obj.isEstimateRange);
  if (isEstimateRange !== (minFare !== maxFare)) return fail();
  return { minFare, maxFare, isEstimateRange };
}

export function parseQuote(value: unknown): Quote {
  const obj = record(value);
  const fare = parseFare(obj.fare);
  const etaMinutes =
    obj.etaMinutes === null ? null : integer(obj.etaMinutes, 0);
  const status = choice(obj.status, [
    "available",
    "partial",
    "unavailable",
    "expired",
  ]);
  if (status === "available" && (fare === null || etaMinutes === null))
    return fail();
  if (status === "partial" && !((fare === null) !== (etaMinutes === null)))
    return fail();
  if (status === "unavailable" && (fare !== null || etaMinutes !== null))
    return fail();
  const fetchedAt = timestamp(obj.fetchedAt),
    expiresAt = timestamp(obj.expiresAt);
  if (Date.parse(expiresAt) <= Date.parse(fetchedAt)) return fail();
  return {
    providerId: text(obj.providerId),
    providerName: text(obj.providerName),
    productId: text(obj.productId),
    productType: choice(obj.productType, [
      "standard",
      "premium",
      "pool",
      "taxi",
    ]),
    serviceName: text(obj.serviceName),
    status,
    currency: choice(obj.currency, ["SGD"]),
    fare,
    etaMinutes,
    source: choice(obj.source, ["simulated", "model", "live", "unknown"]),
    fetchedAt,
    expiresAt,
    cached: boolean(obj.cached),
    capacity: integer(obj.capacity, 1, 6),
    message: text(obj.message, true),
    feesDescription: text(obj.feesDescription, true),
  };
}

export function parseComparison(value: unknown): Comparison {
  const obj = record(value),
    trip = record(obj.trip);
  const passengers = integer(trip.passengers, 1, 6);
  const rideType = choice(trip.rideType, ["standard", "six"]);
  if (passengers > 4 && rideType !== "six") return fail();
  const quotes = list(obj.quotes, 256).map(parseQuote);
  const keys = new Set<string>();
  for (const quote of quotes) {
    const key = JSON.stringify([quote.providerId, quote.productId]);
    if (keys.has(key)) return fail();
    keys.add(key);
  }
  const collectedAt = timestamp(obj.collectedAt),
    expiresAt = timestamp(obj.expiresAt);
  if (Date.parse(expiresAt) <= Date.parse(collectedAt)) return fail();
  const route = parseRoute(trip.route);
  const distanceKm = number(trip.distanceKm, 0);
  if (Math.abs(distanceKm - route.distanceMeters / 1000) > 1e-9) return fail();
  return {
    mode: choice(obj.mode, ["demo"]),
    notice: text(obj.notice),
    trip: {
      pickup: parseLocation(trip.pickup),
      dropoff: parseLocation(trip.dropoff),
      passengers,
      rideType,
      scenario: choice(trip.scenario, ["normal", "busy", "rain", "outage"]),
      distanceKm,
      route,
    },
    quotes,
    collectedAt,
    expiresAt,
  };
}

export function parseRoute(value: unknown): Route {
  const obj = record(value);
  const source = choice(obj.source, ["approximate", "onemap"]);
  const durationSeconds =
    obj.durationSeconds === null ? null : number(obj.durationSeconds, 0);
  if (
    obj.trafficAware !== false ||
    (source === "onemap" && durationSeconds === null) ||
    (source === "approximate" && durationSeconds !== null)
  )
    return fail();
  return {
    source,
    distanceMeters: number(obj.distanceMeters, 0),
    durationSeconds,
    trafficAware: false,
  };
}

export function parseLocations(value: unknown) {
  const obj = record(value);
  const locations = list(obj.locations, 1000).map(parseLocation);
  if (
    new Set(locations.map((location) => location.id)).size !== locations.length
  )
    return fail();
  return {
    locations,
    routingSource: choice(obj.routingSource, ["approximate", "onemap"]),
  };
}

export function parseCurrentLocation(value: unknown): {
  location: Location;
  source: "device";
  persisted: false;
} {
  const obj = record(value),
    location = parseLocation(obj.location);
  if (
    obj.source !== "device" ||
    obj.persisted !== false ||
    location.id !== "current-location"
  )
    return fail();
  return { location, source: "device", persisted: false };
}
