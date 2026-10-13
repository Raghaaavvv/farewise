import type { ProductType, Quote } from "./contracts";

export type ProductFilter = ProductType | "all";
export function quoteKey(q: Quote): string {
  return JSON.stringify([q.providerId, q.productId]);
}
export function quoteExpired(q: Quote, now: number): boolean {
  return q.status === "expired" || now >= Date.parse(q.expiresAt);
}
export function priced(q: Quote, now: number): boolean {
  return q.fare !== null && q.status !== "unavailable" && !quoteExpired(q, now);
}
export function sortedQuotes(
  quotes: Quote[],
  product: ProductFilter,
  sort: string,
  now: number,
): Quote[] {
  return quotes
    .filter((q) => product === "all" || q.productType === product)
    .sort((a, b) => {
      const usableA = priced(a, now),
        usableB = priced(b, now);
      if (usableA !== usableB) return usableA ? -1 : 1;
      const valueA = sort === "eta" ? a.etaMinutes : a.fare?.minFare;
      const valueB = sort === "eta" ? b.etaMinutes : b.fare?.minFare;
      if (valueA !== valueB) return (valueA ?? Infinity) - (valueB ?? Infinity);
      return quoteKey(a).localeCompare(quoteKey(b));
    });
}

// Recommend only if one product's entire range is below every comparable
// alternative. Overlapping ranges and mixing categories get no winner badge.
export function lowestQuote(
  quotes: Quote[],
  product: ProductFilter,
  now: number,
): Quote | null {
  const candidates = quotes.filter((q) => priced(q, now));
  if (product === "all" || candidates.length < 2) return null;
  return (
    candidates.find((q) =>
      candidates.every(
        (other) =>
          quoteKey(q) === quoteKey(other) ||
          q.fare!.maxFare < other.fare!.minFare,
      ),
    ) ?? null
  );
}
