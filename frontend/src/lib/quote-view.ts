// Sort, filter and compare quote ranges without recommending stale or incomparable products.
import type { ProductType, Quote } from "./contracts";

export type ProductFilter = ProductType | "all";

/** Identify a product by both provider and product IDs. */
export function quoteKey(quote: Quote): string {
  return JSON.stringify([quote.providerId, quote.productId]);
}

/** Check expiry using both the backend status and the quote timestamp. */
export function quoteExpired(quote: Quote, now: number): boolean {
  return quote.status === "expired" || now >= Date.parse(quote.expiresAt);
}

/** Accept only a priced product that is available for comparison and has not expired. */
export function priced(quote: Quote, now: number): boolean {
  return (
    quote.fare !== null &&
    quote.status !== "unavailable" &&
    !quoteExpired(quote, now)
  );
}

/** Sort a filtered copy so usable products appear before products missing current data. */
export function sortedQuotes(
  quotes: Quote[],
  product: ProductFilter,
  sort: string,
  now: number,
): Quote[] {
  return quotes
    .filter((quote) => product === "all" || quote.productType === product)
    .sort((firstQuote, secondQuote) => {
      const usableA = priced(firstQuote, now);

      const usableB = priced(secondQuote, now);

      if (usableA !== usableB) {
        if (usableA) {
          return -1;
        }

        return 1;
      }

      const valueA =
        sort === "eta" ? firstQuote.etaMinutes : firstQuote.fare?.minFare;

      const valueB =
        sort === "eta" ? secondQuote.etaMinutes : secondQuote.fare?.minFare;

      if (valueA !== valueB) {
        return (valueA ?? Infinity) - (valueB ?? Infinity);
      }

      return quoteKey(firstQuote).localeCompare(quoteKey(secondQuote));
    });
}

// Recommend only if one product's entire range is below every comparable
// alternative. Overlapping ranges and mixing categories get no winner badge.
export function lowestQuote(
  quotes: Quote[],
  product: ProductFilter,
  now: number,
): Quote | null {
  const candidates = quotes.filter((quote) => priced(quote, now));

  if (product === "all" || candidates.length < 2) {
    return null;
  }

  return (
    candidates.find((quote) =>
      candidates.every(
        (other) =>
          quoteKey(quote) === quoteKey(other) ||
          quote.fare!.maxFare < other.fare!.minFare,
      ),
    ) ?? null
  );
}
