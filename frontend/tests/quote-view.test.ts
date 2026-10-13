import assert from "node:assert/strict";
import test from "node:test";
import { fareLabel } from "../src/lib/api";
import type { Quote } from "../src/lib/contracts";
import {
  lowestQuote,
  quoteExpired,
  quoteKey,
  sortedQuotes,
} from "../src/lib/quote-view";

const now = Date.parse("2026-10-14T10:00:30Z");
function quote(id: string, min: number, max = min): Quote {
  return {
    providerId: id,
    providerName: id,
    productId: "standard",
    productType: "standard",
    serviceName: "Standard",
    status: "available",
    currency: "SGD",
    fare: { minFare: min, maxFare: max, isEstimateRange: min !== max },
    etaMinutes: 4,
    source: "simulated",
    fetchedAt: "2026-10-14T10:00:00Z",
    expiresAt: "2026-10-14T10:01:00Z",
    cached: false,
    capacity: 4,
    message: "Demo",
    feesDescription: "Invented",
  };
}

test("overlapping ranges have no lowest-fare badge; separated ranges can", () => {
  assert.equal(
    lowestQuote(
      [quote("a", 1800, 2500), quote("b", 2000, 2200)],
      "standard",
      now,
    ),
    null,
  );
  assert.equal(
    lowestQuote(
      [quote("a", 1800, 1900), quote("b", 2000, 2200)],
      "standard",
      now,
    )?.providerId,
    "a",
  );
  assert.equal(
    lowestQuote([quote("a", 1800), quote("b", 1800)], "standard", now),
    null,
  );
  assert.equal(
    lowestQuote([quote("a", 1800), quote("b", 2200)], "all", now),
    null,
  );
});

test("category filtering and product identity keep tiers from one provider distinct", () => {
  const standard = quote("a", 1800),
    premium = {
      ...quote("a", 2400),
      productId: "premium",
      productType: "premium" as const,
    };
  assert.notEqual(quoteKey(standard), quoteKey(premium));
  assert.deepEqual(sortedQuotes([standard, premium], "premium", "price", now), [
    premium,
  ]);
});

test("expired products cannot win, while a later-expiring sibling remains usable", () => {
  const old = { ...quote("old", 1000), expiresAt: "2026-10-14T10:00:20Z" };
  const fresh = quote("fresh", 1800),
    other = quote("other", 2200);
  assert.equal(quoteExpired(old, now), true);
  assert.equal(
    lowestQuote([old, fresh, other], "standard", now)?.providerId,
    "fresh",
  );
  assert.deepEqual(
    sortedQuotes([old, fresh], "standard", "price", now).map(
      (q) => q.providerId,
    ),
    ["fresh", "old"],
  );
});

test("partial quotes keep prices; missing ETAs sort after known pickup times", () => {
  const partial = {
    ...quote("partial", 1500),
    status: "partial" as const,
    etaMinutes: null,
  };
  const complete = quote("complete", 1800);
  assert.equal(
    sortedQuotes([partial, complete], "standard", "price", now)[0].providerId,
    "partial",
  );
  assert.equal(
    sortedQuotes([partial, complete], "standard", "eta", now)[0].providerId,
    "complete",
  );
});

test("fare display collapses equal bounds and preserves ranges and missing prices", () => {
  assert.equal(fareLabel(quote("a", 1800).fare), "$18.00");
  assert.equal(fareLabel(quote("a", 1800, 2300).fare), "$18.00 – $23.00");
  assert.equal(fareLabel(null), "—");
});
