import assert from "node:assert/strict";
import test from "node:test";
import {
  parsePrice,
  singaporeInput,
  singaporeISO,
  parseSnapshot,
  parseObservation,
  parsePage,
  requestForSnapshot,
  capture,
  save,
} from "../src/lib/observations";
import { ResponseValidationError } from "../src/lib/contracts";
const session = "11111111-1111-4111-8111-111111111111";
const location = {
  id: "current-location",
  name: "Test pickup",
  detail: "Test",
  latitude: 1.3,
  longitude: 103.8,
};
const snapshot = {
  id: session,
  capturedAt: "2026-10-14T10:00:00Z",
  featureVersion: "route-traffic-v1",
  demoVersion: "demo-v1",
  pickupZone: "residential",
  dropoffZone: "retail",
  demoQuotes: [],
  trip: {
    pickup: location,
    dropoff: {
      ...location,
      id: "map-location",
      name: "Test destination",
      longitude: 103.9,
    },
    passengers: 1,
    rideType: "standard",
    scenario: "normal",
    distanceKm: 20,
    route: {
      source: "onemap",
      distanceMeters: 20000,
      durationSeconds: 1200,
      trafficAware: false,
    },
  },
};
const record = {
  id: "22222222-2222-4222-8222-222222222222",
  sessionId: session,
  provider: "grab",
  product: "JustGrab",
  priceCents: 3700,
  fareKind: "quoted",
  quotedAt: "2026-10-14T10:01:00Z",
  weather: "dry",
  promotion: "none",
  includedFeesCents: null,
  notes: "",
  revision: 1,
  createdAt: "2026-10-14T10:02:00Z",
  updatedAt: "2026-10-14T10:02:00Z",
  snapshot,
  contextAligned: true,
  trafficFreshAtQuote: false,
  trainingEligible: true,
  singaporeWeekday: 3,
  timeSlot: "evening",
};
test("observation prices use exact integer cents and reject accidental formats", () => {
  assert.equal(parsePrice("13.50"), 1350);
  assert.equal(parsePrice("68"), 6800);
  assert.equal(parsePrice("0.01"), 1);
  for (const value of ["", "-1", "1e2", "13.501", "abc", "10000.01"]) {
    assert.throws(() => parsePrice(value));
  }
});
test("observation timestamps always use Singapore time independent of device timezone", () => {
  assert.equal(
    singaporeInput(new Date("2026-10-14T10:00:00Z")),
    "2026-10-14T18:00:00",
  );
  assert.equal(singaporeISO("2026-10-14T18:00"), "2026-10-14T10:00:00.000Z");
  assert.throws(() => singaporeISO("2026-02-31T18:00"));
});
test("observation snapshots preserve precise coordinates and independent provenance", () => {
  const s = parseSnapshot(snapshot);
  const trip = requestForSnapshot(s);
  assert.equal(trip.pickupCoordinates?.longitude, 103.8);
  assert.equal(trip.dropoffCoordinates?.longitude, 103.9);
  assert.equal(
    parseObservation(record).snapshot.featureVersion,
    "route-traffic-v1",
  );
  for (const value of [
    { ...record, sessionId: "33333333-3333-4333-8333-333333333333" },
    { ...record, priceCents: 37.5 },
    { ...record, trainingEligible: false },
    { ...record, fareKind: "paid" },
    { ...record, updatedAt: "2026-10-14T09:00:00Z" },
  ])
    assert.throws(() => parseObservation(value), ResponseValidationError);
});
test("observation list validates counts and rejects duplicate IDs", () => {
  const page = {
    records: [record],
    total: 1,
    stats: {
      total: 1,
      quotes: 1,
      paid: 0,
      routes: 1,
      byProvider: [{ label: "grab", count: 1 }],
      byTimeSlot: [{ label: "evening", count: 1 }],
      byWeekday: [{ label: "3", count: 1 }],
    },
  };
  assert.equal(parsePage(page).records.length, 1);
  assert.throws(
    () => parsePage({ ...page, records: [record, record], total: 2 }),
    ResponseValidationError,
  );
});
test("observation API forwards cancellation and rejects malformed successful saves", async () => {
  const original = globalThis.fetch;
  const controller = new AbortController();
  globalThis.fetch = async (url, init) => {
    assert.equal(init?.signal, controller.signal);
    assert.equal(init?.method, "POST");
    return new Response(
      JSON.stringify(String(url).includes("sessions") ? snapshot : {}),
      { status: 200 },
    );
  };
  try {
    await capture(
      {
        id: session,
        trip: requestForSnapshot(parseSnapshot(snapshot)),
        pickupLabel: "Test",
        dropoffLabel: "Test",
        pickupZone: "unknown",
        dropoffZone: "unknown",
      },
      controller.signal,
    );
    const input = {
      id: record.id,
      sessionId: session,
      provider: "grab" as const,
      product: "JustGrab",
      priceCents: 3700,
      fareKind: "quoted" as const,
      quotedAt: record.quotedAt,
      weather: "dry" as const,
      promotion: "none" as const,
      includedFeesCents: null,
      notes: "",
    };
    await assert.rejects(
      save(input, controller.signal),
      ResponseValidationError,
    );
  } finally {
    globalThis.fetch = original;
  }
});
