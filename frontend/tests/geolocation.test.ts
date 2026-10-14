// Inject browser geolocation behavior so these tests never request real device coordinates.
import assert from "node:assert/strict";

import test from "node:test";

import { getDeviceCoordinates } from "../src/lib/geolocation";

test("permission success preserves coordinates and accuracy", async () => {
  const geolocation: Pick<Geolocation, "getCurrentPosition"> = {
    getCurrentPosition(success, _error, options) {
      assert.equal(options?.enableHighAccuracy, true);

      assert.equal(options?.maximumAge, 0);

      assert.equal(options?.timeout, 15000);

      success({
        coords: { latitude: 1.36, longitude: 103.989, accuracy: 12 },
      } as GeolocationPosition);
    },
  };

  assert.deepEqual(await getDeviceCoordinates({ secure: true, geolocation }), {
    latitude: 1.36,
    longitude: 103.989,
    accuracyMeters: 12,
  });
});

for (const [code, expected] of [
  [1, /permission was denied/],
  [2, /could not determine/],
  [3, /timed out/],
] as const) {
  test(`location failure ${code} gives an actionable manual fallback`, async () => {
    const geolocation: Pick<Geolocation, "getCurrentPosition"> = {
      getCurrentPosition(_success, error) {
        error?.({ code } as GeolocationPositionError);
      },
    };

    await assert.rejects(
      getDeviceCoordinates({ secure: true, geolocation }),
      expected,
    );
  });
}

test("insecure origins do not request device permission", async () => {
  let called = false;

  const geolocation: Pick<Geolocation, "getCurrentPosition"> = {
    getCurrentPosition() {
      called = true;
    },
  };

  await assert.rejects(
    getDeviceCoordinates({ secure: false, geolocation }),
    /HTTPS or localhost/,
  );

  assert.equal(called, false);
});

test("unsupported browsers retain manual selection", async () => {
  await assert.rejects(
    getDeviceCoordinates({ secure: true }),
    /does not support/,
  );
});
