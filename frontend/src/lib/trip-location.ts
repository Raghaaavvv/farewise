import type { DeviceCoordinates, Location, TripRequest } from "./api";

export type Endpoint = "pickup" | "dropoff";
export type DevicePosition = {
  location: Location;
  coordinates: DeviceCoordinates;
};

// Each endpoint owns its snapshot. Capturing drop-off must not replace pickup.
export function withDeviceLocation(
  trip: TripRequest,
  endpoint: Endpoint,
  coordinates: DeviceCoordinates,
): TripRequest {
  return endpoint === "pickup"
    ? {
        ...trip,
        pickupId: "current-location",
        pickupCoordinates: { ...coordinates },
      }
    : {
        ...trip,
        dropoffId: "current-location",
        dropoffCoordinates: { ...coordinates },
      };
}

export function swapTrip(trip: TripRequest): TripRequest {
  return {
    ...trip,
    pickupId: trip.dropoffId,
    dropoffId: trip.pickupId,
    pickupCoordinates: trip.dropoffCoordinates,
    dropoffCoordinates: trip.pickupCoordinates,
  };
}

export function sameEndpoints(trip: TripRequest, catalog: Location[]): boolean {
  const pickup =
    trip.pickupId === "current-location"
      ? trip.pickupCoordinates
      : catalog.find((l) => l.id === trip.pickupId);
  const dropoff =
    trip.dropoffId === "current-location"
      ? trip.dropoffCoordinates
      : catalog.find((l) => l.id === trip.dropoffId);
  if (pickup && dropoff)
    return (
      pickup.latitude === dropoff.latitude &&
      pickup.longitude === dropoff.longitude
    );
  return trip.pickupId === trip.dropoffId;
}
