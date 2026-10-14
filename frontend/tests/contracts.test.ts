// Check the runtime comparison contract against valid and malformed provider data.
import assert from "node:assert/strict";

import test from "node:test";

import {
  compareTrip,
  fetchLocations,
  initialTrip,
  resolveCurrentLocation,
} from "../src/lib/api";

import {
  parseComparison,
  parseQuote,
  parseRoute,
  parseLocations,
  ResponseValidationError,
} from "../src/lib/contracts";

const location = {
  id: "orchard",
  name: "Orchard",
  detail: "Demo",
  latitude: 1.304,
  longitude: 103.832,
};

function quote(overrides: Record<string, unknown> = {}) {
  return {
    providerId: "demo",
    providerName: "Demo",
    productId: "standard",
    productType: "standard",
    serviceName: "Standard",
    status: "available",
    currency: "SGD",
    fare: { minFare: 1800, maxFare: 1800, isEstimateRange: false },
    etaMinutes: 4,
    source: "simulated",
    fetchedAt: "2026-10-14T10:00:00Z",
    expiresAt: "2026-10-14T10:01:00Z",
    cached: false,
    capacity: 4,
    message: "Demo",
    feesDescription: "Invented",
    ...overrides,
  };
}

function comparison(quotes: unknown[] = [quote()]) {
  return {
    mode: "demo",
    notice: "Simulated",
    trip: {
      pickup: location,
      dropoff: {
        ...location,
        id: "changi",
        latitude: 1.357,
        longitude: 103.987,
      },
      passengers: 1,
      rideType: "standard",
      scenario: "normal",
      distanceKm: 18.3,
      route: {
        source: "approximate",
        distanceMeters: 18300,
        durationSeconds: null,
        trafficAware: false,
      },
    },
    quotes,
    collectedAt: "2026-10-14T10:00:00Z",
    expiresAt: "2026-10-14T10:01:00Z",
  };
}

test("contract accepts multiple products, ranges, partial fields and unavailable results", () => {
  const result = parseComparison(
    comparison([
      quote(),
      quote({
        productId: "premium",
        productType: "premium",
        fare: { minFare: 2200, maxFare: 2600, isEstimateRange: true },
      }),
      quote({ providerId: "other", status: "partial", etaMinutes: null }),
      quote({
        productId: "taxi",
        productType: "taxi",
        status: "partial",
        fare: null,
      }),
      quote({
        productId: "unavailable",
        status: "unavailable",
        fare: null,
        etaMinutes: null,
      }),
    ]),
  );

  assert.equal(result.quotes.length, 5);

  assert.equal(result.quotes[1].fare?.maxFare, 2600);

  assert.equal(result.quotes[2].etaMinutes, null);
});

test("contract accepts an explicit zero fare and ignores additive fields", () => {
  const result = parseQuote(
    quote({
      fare: { minFare: 0, maxFare: 0, isEstimateRange: false },
      futureField: "extra",
    }),
  );

  assert.equal(result.fare?.minFare, 0);

  assert.equal("futureField" in result, false);
});

test("contract rejects malformed fare ranges and missing price fields", () => {
  for (const fare of [
    undefined,
    "1800",
    { minFare: -1, maxFare: 2, isEstimateRange: true },
    { minFare: 2000, maxFare: 1800, isEstimateRange: true },
    { minFare: 1.5, maxFare: 2, isEstimateRange: true },
    { minFare: 1800, maxFare: 1800, isEstimateRange: true },
    { minFare: 1800, maxFare: 2200, isEstimateRange: false },
    { minFare: 0, maxFare: Number.MAX_SAFE_INTEGER + 1, isEstimateRange: true },
  ]) {
    assert.throws(() => parseQuote(quote({ fare })), ResponseValidationError);
  }
});

test("contract rejects contradictory statuses, identifiers, types and timestamps", () => {
  for (const overrides of [
    { status: "available", etaMinutes: null },
    { status: "unavailable" },
    { status: "partial" },
    { status: "partial", fare: null, etaMinutes: null },
    { status: "unexpected" },
    { productType: "bus" },
    { productId: "" },
    { providerName: null },
    { capacity: 0 },
    { etaMinutes: -1 },
    { etaMinutes: "4" },
    { currency: "USD" },
    { source: "unknown-source" },
    { cached: "false" },
    { fetchedAt: "yesterday" },
    { fetchedAt: "2026-02-30T10:00:00Z" },
    { expiresAt: "2026-10-14T10:00:00Z" },
    { expiresAt: "2026-10-14T09:59:00Z" },
  ]) {
    assert.throws(() => parseQuote(quote(overrides)), ResponseValidationError);
  }
});

test("contract rejects duplicate provider-product keys but allows different products", () => {
  assert.throws(
    () => parseComparison(comparison([quote(), quote()])),
    ResponseValidationError,
  );

  assert.equal(
    parseComparison(
      comparison([
        quote(),
        quote({ productId: "premium", productType: "premium" }),
      ]),
    ).quotes.length,
    2,
  );
});

test("API boundary rejects invalid 200 responses before UI state receives them", async () => {
  const original = globalThis.fetch;

  try {
    globalThis.fetch = async () =>
      new Response(JSON.stringify(comparison([quote({ fare: "1800" })])));

    await assert.rejects(compareTrip(initialTrip), ResponseValidationError);

    globalThis.fetch = async () =>
      new Response(
        JSON.stringify({ locations: [{ ...location, latitude: 91 }] }),
      );

    await assert.rejects(fetchLocations(), ResponseValidationError);

    globalThis.fetch = async () =>
      new Response(
        JSON.stringify({
          location: { ...location, id: "current-location" },
          source: "device",
          persisted: true,
        }),
      );

    await assert.rejects(
      resolveCurrentLocation({ latitude: 1.3, longitude: 103.8 }),
      ResponseValidationError,
    );
  } finally {
    globalThis.fetch = original;
  }
});

test("API boundary handles malformed error JSON and non-JSON responses safely", async () => {
  const original = globalThis.fetch;

  try {
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ error: { message: "bad" } }), {
        status: 502,
      });

    await assert.rejects(compareTrip(initialTrip), /service is unavailable/);

    globalThis.fetch = async () =>
      new Response("<html>Bad gateway</html>", { status: 502 });

    await assert.rejects(compareTrip(initialTrip), /service is unavailable/);
  } finally {
    globalThis.fetch = original;
  }
});

test("comparison validates finite coordinates, passengers and collection expiry", () => {
  const valid = comparison();

  for (const value of [
    null,
    [],
    { ...valid, quotes: {} },
    { ...valid, trip: { ...valid.trip, passengers: 5 } },
    {
      ...valid,
      trip: { ...valid.trip, pickup: { ...location, longitude: Infinity } },
    },
    { ...valid, expiresAt: valid.collectedAt },
  ]) {
    assert.throws(() => parseComparison(value), ResponseValidationError);
  }
});

test("route contract distinguishes real driving distance from approximation", () => {
  assert.deepEqual(
    parseRoute({
      source: "onemap",
      distanceMeters: 23456,
      durationSeconds: 1234.5,
      trafficAware: false,
    }),
    {
      source: "onemap",
      distanceMeters: 23456,
      durationSeconds: 1234.5,
      trafficAware: false,
    },
  );

  const route = comparison().trip.route;

  for (const value of [
    null,
    { ...route, source: "unknown" },
    { ...route, distanceMeters: -1 },
    { ...route, distanceMeters: "18300" },
    { ...route, source: "onemap" },
    { ...route, durationSeconds: 100 },
    { ...route, trafficAware: true },
  ]) {
    assert.throws(() => parseRoute(value), ResponseValidationError);
  }

  const valid = comparison();

  assert.throws(
    () =>
      parseComparison({ ...valid, trip: { ...valid.trip, distanceKm: 20 } }),
    ResponseValidationError,
  );

  assert.equal(
    parseLocations({ locations: [location], routingSource: "onemap" })
      .routingSource,
    "onemap",
  );

  assert.throws(
    () => parseLocations({ locations: [location], routingSource: "unknown" }),
    ResponseValidationError,
  );
});
