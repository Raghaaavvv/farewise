// Check that coordinate selections and swaps preserve the pickup and drop-off snapshots.
import assert from "node:assert/strict";

import test from "node:test";

import { initialTrip } from "../src/lib/api";

import {
  sameEndpoints,
  swapTrip,
  withDeviceLocation,
} from "../src/lib/trip-location";

const a = { latitude: 1.357, longitude: 103.987, accuracyMeters: 10 };

const b = { latitude: 1.304, longitude: 103.832, accuracyMeters: 15 };

test("drop-off capture preserves independently captured pickup coordinates", () => {
  const pickup = withDeviceLocation(initialTrip, "pickup", a);

  const both = withDeviceLocation(pickup, "dropoff", b);

  assert.equal(both.pickupId, "current-location");

  assert.equal(both.dropoffId, "current-location");

  assert.deepEqual(both.pickupCoordinates, a);

  assert.deepEqual(both.dropoffCoordinates, b);

  assert.equal(sameEndpoints(both, []), false);

  const updated = withDeviceLocation(both, "pickup", { ...a, latitude: 1.4 });

  assert.deepEqual(updated.dropoffCoordinates, b);

  assert.equal(both.pickupCoordinates?.latitude, a.latitude);
});

test("swap preserves endpoint snapshots and double swap restores the trip", () => {
  const both = withDeviceLocation(
    withDeviceLocation(initialTrip, "pickup", a),
    "dropoff",
    b,
  );

  const swapped = swapTrip(both);

  assert.deepEqual(swapped.pickupCoordinates, b);

  assert.deepEqual(swapped.dropoffCoordinates, a);

  assert.deepEqual(swapTrip(swapped), both);

  const mixed = withDeviceLocation(initialTrip, "dropoff", b);

  assert.equal(swapTrip(mixed).pickupId, "current-location");

  assert.equal(swapTrip(mixed).dropoffId, "changi-t3");
});

test("identical coordinates are invalid even with different location IDs", () => {
  const both = withDeviceLocation(
    withDeviceLocation(initialTrip, "pickup", a),
    "dropoff",
    a,
  );

  assert.equal(sameEndpoints(both, []), true);

  assert.equal(
    sameEndpoints(withDeviceLocation(initialTrip, "pickup", b), [
      { id: "orchard", name: "Orchard", detail: "", ...b },
    ]),
    true,
  );
});
