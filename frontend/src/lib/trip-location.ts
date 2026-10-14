// Keep coordinates attached to their own endpoint when selecting or swapping locations.
import type { DeviceCoordinates, Location, TripRequest } from "./api";

export type Endpoint = "pickup" | "dropoff";

export type DevicePosition = {
  location: Location;
  coordinates: DeviceCoordinates;
};

/** Identify endpoints whose coordinates come from the device or map rather than the catalog. */
export function isCoordinateLocation(id: string): boolean {
  return id === "current-location" || id === "map-location";
}

/** Replace the destination and its coordinate snapshot together. */
export function withMapLocation(
  trip: TripRequest,
  coordinates: DeviceCoordinates,
): TripRequest {
  return {
    ...trip,
    dropoffId: "map-location",
    dropoffCoordinates: {
      latitude: coordinates.latitude,
      longitude: coordinates.longitude,
    },
  };
}

// Each endpoint owns its snapshot. Capturing drop-off must not replace pickup.
export function withDeviceLocation(
  trip: TripRequest,
  endpoint: Endpoint,
  coordinates: DeviceCoordinates,
): TripRequest {
  if (endpoint === "pickup") {
    return {
      ...trip,
      pickupId: "current-location",
      pickupCoordinates: { ...coordinates },
    };
  }

  return {
    ...trip,
    dropoffId: "current-location",
    dropoffCoordinates: { ...coordinates },
  };
}

/** Swap identifiers and coordinate snapshots together so each endpoint keeps the correct point. */
export function swapTrip(trip: TripRequest): TripRequest {
  return {
    ...trip,
    pickupId: trip.dropoffId,
    dropoffId: trip.pickupId,
    pickupCoordinates: trip.dropoffCoordinates,
    dropoffCoordinates: trip.pickupCoordinates,
  };
}

/** Resolve both endpoint points before checking whether they represent the same location. */
export function sameEndpoints(trip: TripRequest, catalog: Location[]): boolean {
  const pickup = isCoordinateLocation(trip.pickupId)
    ? trip.pickupCoordinates
    : catalog.find((location) => location.id === trip.pickupId);

  const dropoff = isCoordinateLocation(trip.dropoffId)
    ? trip.dropoffCoordinates
    : catalog.find((location) => location.id === trip.dropoffId);

  if (pickup && dropoff) {
    return (
      pickup.latitude === dropoff.latitude &&
      pickup.longitude === dropoff.longitude
    );
  }

  return trip.pickupId === trip.dropoffId;
}
