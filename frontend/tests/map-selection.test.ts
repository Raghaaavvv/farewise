// Check Singapore map bounds, endpoint coordinates and malformed map API responses.
import assert from "node:assert/strict";

import test from "node:test";

import { initialTrip, resolveMapLocation } from "../src/lib/api";

import {
  MAP_BOUNDS,
  mapCoordinateLabel,
  mapPoint,
} from "../src/lib/map-selection";

import {
  parseMapLocation,
  ResponseValidationError,
} from "../src/lib/contracts";

import {
  sameEndpoints,
  swapTrip,
  withDeviceLocation,
  withMapLocation,
} from "../src/lib/trip-location";

const point = { latitude: 1.303452345, longitude: 103.83512345 };

const location = {
  id: "map-location",
  name: "Pinned location",
  detail: "Map coordinates",
  ...point,
};

test("map points keep full coordinate precision while labels are rounded", () => {
  assert.deepEqual(mapPoint(point.latitude, point.longitude), point);

  assert.equal(mapCoordinateLabel(point), "1.30345, 103.83512");

  for (const [lat, lon] of [
    [NaN, 103.8],
    [1.3, Infinity],
    [0, 0],
    [MAP_BOUNDS.north + 0.001, 103.8],
    [1.3, MAP_BOUNDS.east + 0.001],
  ]) {
    assert.equal(mapPoint(lat, lon), null);
  }

  assert.notEqual(mapPoint(MAP_BOUNDS.south, MAP_BOUNDS.west), null);
});

test("map destination preserves pickup and remains valid after swapping", () => {
  const pickup = { latitude: 1.357, longitude: 103.987, accuracyMeters: 10 };

  const trip = withMapLocation(
    withDeviceLocation(initialTrip, "pickup", pickup),
    point,
  );

  assert.equal(trip.dropoffId, "map-location");

  assert.deepEqual(trip.pickupCoordinates, pickup);

  assert.deepEqual(trip.dropoffCoordinates, point);

  const swapped = swapTrip(trip);

  assert.equal(swapped.pickupId, "map-location");

  assert.deepEqual(swapped.pickupCoordinates, point);

  assert.equal(sameEndpoints(swapped, []), false);

  assert.deepEqual(swapTrip(swapped), trip);

  assert.equal(sameEndpoints(withMapLocation(trip, pickup), []), true);
});

test("map response requires map provenance and rejects misleading GPS responses", () => {
  const valid = { location, source: "map", persisted: false };

  assert.equal(parseMapLocation(valid).location.latitude, point.latitude);

  for (const invalid of [
    { ...valid, source: "device" },
    { ...valid, persisted: true },
    { ...valid, location: { ...location, id: "current-location" } },
    { ...valid, location: { ...location, latitude: 91 } },
  ]) {
    assert.throws(() => parseMapLocation(invalid), ResponseValidationError);
  }
});

test("map confirmation uses the coordinate endpoint and forwards cancellation", async () => {
  const original = globalThis.fetch;

  const controller = new AbortController();

  globalThis.fetch = async (path, init) => {
    assert.equal(path, "/api/locations/map");

    assert.equal(init?.method, "POST");

    assert.equal(init?.signal, controller.signal);

    assert.deepEqual(JSON.parse(String(init?.body)), point);

    return new Response(
      JSON.stringify({ location, source: "map", persisted: false }),
    );
  };

  try {
    assert.deepEqual(
      (await resolveMapLocation(point, controller.signal)).location,
      location,
    );
  } finally {
    globalThis.fetch = original;
  }
});
