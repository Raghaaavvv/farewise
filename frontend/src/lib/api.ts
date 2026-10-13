export type Location = {
  id: string;
  name: string;
  detail: string;
  latitude: number;
  longitude: number;
};

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

export type Quote = {
  providerId: string;
  providerName: string;
  serviceName: string;
  status: "available" | "unavailable";
  currency: string;
  amountCents: number | null;
  etaMinutes: number | null;
  source: string;
  collectedAt: string;
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
    rideType: string;
    scenario: string;
    distanceKm: number;
  };
  quotes: Quote[];
  collectedAt: string;
  expiresAt: string;
};

export const initialTrip: TripRequest = {
  pickupId: "changi-t3",
  dropoffId: "orchard",
  passengers: 1,
  rideType: "standard",
  scenario: "normal",
};

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, { ...init, cache: "no-store" });
  let body;
  try {
    body = await response.json();
  } catch {
    throw new Error(
      "The comparison service is unavailable. Check that the Go backend is running, then try again.",
    );
  }
  if (!response.ok)
    throw new Error(
      body.error ?? "The comparison service is unavailable. Please try again.",
    );
  return body as T;
}

export function fetchLocations(signal?: AbortSignal) {
  return request<{ locations: Location[] }>("/api/locations", { signal });
}

export function resolveCurrentLocation(
  coordinates: DeviceCoordinates,
  signal?: AbortSignal,
) {
  return request<{ location: Location; source: "device"; persisted: false }>(
    "/api/locations/current",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(coordinates),
      signal,
    },
  );
}

export function compareTrip(trip: TripRequest, signal?: AbortSignal) {
  return request<Comparison>("/api/compare", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(trip),
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
