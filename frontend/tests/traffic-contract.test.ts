// Check traffic timing, coverage and provenance against the route response contract.
import assert from "node:assert/strict";

import test from "node:test";

import { parseRoute, ResponseValidationError } from "../src/lib/contracts";

const traffic = {
  source: "lta",
  status: "applied",
  baselineSeconds: 1200,
  estimatedSeconds: 1400,
  minSeconds: 1300,
  maxSeconds: 1600,
  coverage: 0.5,
  matchedMeters: 5000,
  matchedLinks: 10,
  observedAt: "2026-10-14T10:00:00Z",
  fetchedAt: "2026-10-14T10:01:00Z",
  expiresAt: "2026-10-14T10:06:00Z",
  message: "Approximate traffic adjustment",
};

const route = {
  source: "onemap",
  distanceMeters: 10000,
  durationSeconds: 1400,
  trafficAware: true,
  traffic,
};

test("route accepts coherent traffic ranges, coverage and baseline fallback", () => {
  assert.equal(parseRoute(route).traffic?.coverage, 0.5);

  const fallback = parseRoute({
    ...route,
    durationSeconds: 1200,
    trafficAware: false,
    traffic: {
      ...traffic,
      status: "insufficient_coverage",
      coverage: 0.1,
      matchedMeters: 1000,
      estimatedSeconds: null,
      minSeconds: null,
      maxSeconds: null,
    },
  });

  assert.equal(fallback.durationSeconds, 1200);

  assert.equal(fallback.trafficAware, false);
});

test("runtime contract rejects contradictory traffic estimates", () => {
  for (const invalid of [
    { ...route, traffic: undefined },
    { ...route, trafficAware: false },
    { ...route, durationSeconds: 1800 },
    { ...route, traffic: { ...traffic, coverage: 1.1 } },
    { ...route, traffic: { ...traffic, matchedMeters: 7000 } },
    { ...route, traffic: { ...traffic, minSeconds: 1450 } },
    { ...route, traffic: { ...traffic, maxSeconds: 1350 } },
    { ...route, traffic: { ...traffic, expiresAt: traffic.fetchedAt } },
    { ...route, traffic: { ...traffic, observedAt: null } },
    { ...route, source: "approximate", durationSeconds: null },
  ]) {
    assert.throws(() => parseRoute(invalid), ResponseValidationError);
  }
});

test("route accepts an explicit unavailable traffic status without invented duration", () => {
  assert.equal(
    parseRoute({
      ...route,
      durationSeconds: 1200,
      trafficAware: false,
      traffic: {
        ...traffic,
        status: "unavailable",
        estimatedSeconds: null,
        minSeconds: null,
        maxSeconds: null,
        observedAt: null,
        fetchedAt: null,
        expiresAt: null,
        coverage: 0,
        matchedMeters: 0,
        matchedLinks: 0,
      },
    }).durationSeconds,
    1200,
  );
});
