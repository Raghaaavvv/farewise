import {
  isRecord,
  parseComparison,
  parseCurrentLocation,
  parseLocations,
  parseMapLocation,
} from "./contracts";
import type { FareRange } from "./contracts";
export type {
  Location,
  ProductType,
  FareRange,
  Quote,
  Comparison,
  Route,
  RoutingSource,
} from "./contracts";

export type TripRequest = {
  pickupId: string;
  dropoffId: string;
  passengers: number;
  rideType: "standard" | "six";
  scenario: "normal" | "busy" | "rain" | "outage";
  pickupCoordinates?: DeviceCoordinates;
  dropoffCoordinates?: DeviceCoordinates;
};

export type DeviceCoordinates = {
  latitude: number;
  longitude: number;
  accuracyMeters?: number;
};

export const initialTrip: TripRequest = {
  pickupId: "changi-t3",
  dropoffId: "orchard",
  passengers: 1,
  rideType: "standard",
  scenario: "normal",
};

async function request<T>(
  path: string,
  parse: (value: unknown) => T,
  init: RequestInit = {},
): Promise<T> {
  const response = await fetch(path, { ...init, cache: "no-store" });
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new Error(
      "The comparison service is unavailable. Check that the Go backend is running, then try again.",
    );
  }
  if (!response.ok)
    throw new Error(
      isRecord(body) && typeof body.error === "string"
        ? body.error
        : "The comparison service is unavailable. Please try again.",
    );
  return parse(body);
}

export function fetchLocations(signal?: AbortSignal) {
  return request("/api/locations", parseLocations, { signal });
}

export function resolveCurrentLocation(
  coordinates: DeviceCoordinates,
  signal?: AbortSignal,
) {
  return request("/api/locations/current", parseCurrentLocation, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(coordinates),
    signal,
  });
}

export function compareTrip(trip: TripRequest, signal?: AbortSignal) {
  return request("/api/compare", parseComparison, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(trip),
    signal,
  });
}

export function resolveMapLocation(
  coordinates: DeviceCoordinates,
  signal?: AbortSignal,
) {
  return request("/api/locations/map", parseMapLocation, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(coordinates),
    signal,
  });
}

export function money(cents: number) {
  return new Intl.NumberFormat("en-SG", {
    style: "currency",
    currency: "SGD",
    currencyDisplay: "narrowSymbol",
  }).format(cents / 100);
}

export function fareLabel(fare: FareRange | null): string {
  if (!fare) return "—";
  return fare.isEstimateRange
    ? `${money(fare.minFare)} – ${money(fare.maxFare)}`
    : money(fare.minFare);
}
