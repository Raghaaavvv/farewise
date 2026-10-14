// Check device-location HTTP requests, cancellation signals and response validation.
import assert from "node:assert/strict";

import test from "node:test";

import { resolveCurrentLocation } from "../src/lib/api";

test("device position is sent to the backend in a POST body", async () => {
  const originalFetch = globalThis.fetch;

  const coordinates = {
    latitude: 1.36,
    longitude: 103.989,
    accuracyMeters: 12,
  };

  globalThis.fetch = async (path, init) => {
    assert.equal(path, "/api/locations/current");

    assert.equal(init?.method, "POST");

    assert.equal(init?.cache, "no-store");

    assert.deepEqual(JSON.parse(String(init?.body)), coordinates);

    return new Response(
      JSON.stringify({
        location: {
          id: "current-location",
          name: "Current location",
          detail: "Device coordinates",
          ...coordinates,
        },
        source: "device",
        persisted: false,
      }),
      { status: 200 },
    );
  };

  try {
    assert.equal(
      (await resolveCurrentLocation(coordinates)).location.id,
      "current-location",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("backend coordinate errors reach the UI error handler", async () => {
  const originalFetch = globalThis.fetch;

  globalThis.fetch = async () =>
    new Response(
      JSON.stringify({ error: "latitude must be between -90 and 90" }),
      { status: 400 },
    );

  try {
    await assert.rejects(
      resolveCurrentLocation({ latitude: 91, longitude: 104 }),
      /latitude must/,
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});
