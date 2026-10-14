# Farewise

[![CI](https://github.com/Raghaaavvv/farewise/actions/workflows/ci.yml/badge.svg)](https://github.com/Raghaaavvv/farewise/actions/workflows/ci.yml)

**A Singapore ride comparison prototype built with Go, Next.js and TypeScript.**

Farewise started with a familiar problem: after landing at the airport, finding a ride home meant switching between Grab, Gojek, CDG Zig and Ryde to compare their prices. The intended app brings comparable ride options into one screen so a user can choose based on fare, pickup time and vehicle capacity.

The current prototype demonstrates the complete frontend-to-backend comparison flow using simulated providers. **Every fare, pickup estimate and availability result is invented. No provider servers are contacted, and the app cannot book rides.** A working comparison interface does not yet mean that live fare access has been solved.

![Farewise OneMap destination picker](docs/images/map-picker.png)

[View the iPhone map picker](docs/images/map-picker-iphone.png)

## What works today

- Compare four configured demo providers: Grab, Gojek, CDG Zig and Ryde.
- Receive multiple products from one provider, with standard, premium, pool and taxi categories; filter categories without making another request.
- Display exact fares or min/max ranges, preserve useful partial data, and show each product's expiry independently.
- Choose pickup and destination from nine sample Singapore locations, or swap them.
- Use **Use current location** separately for pickup or drop-off: request browser permission, validate the coordinates through Go, and preserve independent endpoint snapshots when swapping.
- Fetch Singapore driving distance and estimated travel time from OneMap when configured; otherwise explicitly show approximate distance with no invented driving time. Fares remain simulated in both modes.
- Select one to six passengers and standard or six-seater vehicles. More than four passengers automatically selects a six-seater.
- Sort results by the lowest fare or soonest pickup.
- Explore everyday, busy-hour, rainy-day and provider-failure scenarios.
- See loading states, validation errors, retry controls, quote details and expiration notices.
- Keep healthy results visible when one provider is unavailable or times out.
- Protect each demo provider with a token bucket, a 30-second success cache, shared in-progress lookups and bounded retries.
- Bound provider evaluations across simultaneous comparisons, validate API responses at runtime, and emit structured provider logs.
- Use a responsive interface on desktop or iPhone.

Prices are deterministic for the same inputs. Refreshing within the cache window reuses complete successful product batches and their original timestamps; cache entries expire after at most 30 seconds, or sooner when any product expires. Demo product lifetimes intentionally vary from 40 to 120 seconds. The rainy-day Gojek demo omits its pickup ETA to exercise partial data. All vehicles, tiers and service labels are illustrative, rather than verified provider products.

## Languages, frameworks and tools

| Technology | Role in the project |
| --- | --- |
| **Go** | Backend domain models, HTTP API, comparison service, concurrency and tests |
| **Go `net/http`** | Method-based routing, request decoding, response handling and server lifecycle; no external Go web framework |
| **Next.js 16.4** | React App Router frontend and a same-origin proxy to the Go API |
| **React** | Components, form state, asynchronous comparison state and interactive results |
| **TypeScript** | Typed requests, responses, locations, quotes and UI state |
| **HTML, CSS and SVG** | Semantic controls, responsive layouts, icons and a sourced journey-distance summary |
| **Leaflet 1.9.4** | Lazily loaded interactive OneMap destination picker with pan/zoom, draggable pins, keyboard selection and visible attribution |
| **Python 3 (standard library)** | Optional local OneMap token setup and configuration tests; not a fare model |
| **OneMap REST API** | Optional Singapore driving-route distance and duration, called only from Go with a backend token |
| **pnpm** | Frontend dependency management and reproducible lockfile-based installs |
| **Prettier** | Frontend source formatting |
| **Go `testing` / `httptest`** | Unit and HTTP handler tests without external test dependencies |
| **GitHub Actions** | Automatic Go checks, TypeScript checks and production frontend builds |
| **VS Code** | Workspace, recommended extensions, run tasks and a Go debugging configuration |

There is no database, login system, external geocoder or live provider integration in this version. Next.js forwards requests; the comparison business logic lives in Go.

## Architecture and request pipeline

```mermaid
flowchart TD
    A[User selects a trip and clicks Compare rides] --> B[React form state]
    B --> C[TypeScript API client: POST /api/compare]
    C --> D[Next.js same-origin API proxy]
    D --> E[Go HTTP handler: decode and validate]
    E --> F[Resolve catalog or independent device coordinates]
    F --> Y{Routing configuration}
    Y -->|OneMap enabled| Z[One driving-route request: distance and duration]
    Y -->|Offline demo| AA[Labelled Haversine approximation]
    Z -->|Success| G[Comparison service]
    Z -->|Failure| AB[Return routing error without estimating fares]
    AA --> G
    G --> H[Bounded workers and shared provider semaphore]
    H --> U[Independent protection wrapper for each provider]
    U --> V[Success cache and shared in-progress lookup]
    V -->|Cache miss| W[Token bucket and cooldown before every attempt]
    W --> I[Grab demo instance]
    W --> J[Gojek demo instance]
    W --> K[CDG Zig demo instance]
    W --> L[Ryde demo instance]
    V -->|Cache hit| M
    W --> X[Transient HTTP error: bounded retries and Retry-After]
    X --> W
    I --> M[Collect products within comparison deadline]
    J --> M
    K --> M
    L --> M
    M --> N[Normalize quotes and sort available fares]
    N --> O[JSON response through Next.js]
    O --> P[React renders cards or errors]
    Q[Click Use current location] --> R[Browser requests permission and obtains coordinates]
    R --> S[POST /api/locations/current]
    S --> T[Go validates coordinates without saving them]
    T --> B
    AC[Choose drop-off on map] --> AD[Lazy-load Leaflet and public OneMap tiles]
    AD --> AE[Tap or drag a pin, then confirm]
    AE --> AF[POST /api/locations/map validates map coordinates]
    AF --> B
```

The service starts at most four workers per comparison and uses a shared semaphore to permit at most four simultaneous `Provider.Estimate` evaluations across all comparisons on that service instance. A five-second total comparison budget includes worker queueing, semaphore waits, cooldowns, retries and adapter execution. A canceled queue does not start more provider calls; completed healthy products remain in the result.

Provider calls run directly in the workers: a permit is released only when the adapter actually exits. There is no detached goroutine that merely hides a continuing request after timeout. Adapters must honor their context internally; Go cannot forcibly terminate an arbitrary implementation that ignores it. Demo latency uses a timer selected against `ctx.Done()`, and tests exercise actual HTTP request cancellation with `http.NewRequestWithContext`, including cancellation observed by the fake server. `WithMaxConcurrentProviders(n)` configures the service limit; limits are local to that instance/process.

### How buttons reach the backend

| UI action | What happens |
| --- | --- |
| Initial page load | Fetches `GET /api/locations`, then sends the default trip to `POST /api/compare` |
| **Compare rides** | Prevents the normal form submission, captures the current trip, sends JSON, displays loading placeholders, then replaces results |
| **Use current location** | Calls browser geolocation after the click; sends latitude, longitude and accuracy to `POST /api/locations/current`; selects the returned location and asks the user to compare again |
| **Choose on map** | Opens a OneMap basemap; tap a point or choose the center, adjust the pin, then confirm through `POST /api/locations/map`; no GPS prompt or routing request occurs on confirmation |
| **Cancel location lookup** | Aborts the backend request if started, ignores late browser callbacks, and restores manual selection; it cannot dismiss the browser's own permission prompt |
| **Refresh comparison** or **Retry** | Calls the same comparison function with current form values; successful quotes are reused during the 30-second backend cache window |
| Change route, passengers, ride type or scenario | Updates local state; previous results remain visible with a notice until comparison is requested again |
| Change sort order | Reorders the existing response in the browser; does not make another provider request |
| Change vehicle category | Filters already-returned products; does not make another provider request |
| Open quote details | Displays a dialog using fields already returned by the backend |

`AbortController` cancels superseded browser requests. The UI only processes a completion if its controller has not been aborted, preventing a previous trip's response from replacing a newer comparison. Canceling a browser request does not guarantee that an upstream request has already been stopped.

The browser requests relative `/api/...` URLs. Next.js rewrites them to the Go server, avoiding the need for a separate browser-facing API origin during development.

## Software engineering skills demonstrated

### Domain modeling and object-oriented concepts in Go

Go uses **structs, methods and interfaces** rather than Java-style classes and inheritance. This project demonstrates encapsulation, abstraction, polymorphism and composition using those Go mechanisms.

| Model | Relevant fields and responsibility |
| --- | --- |
| `Location` | ID, name, description, latitude and longitude |
| `Request` | Pickup/destination IDs, passenger count, vehicle type and demo scenario |
| `Coordinates` | Optional device latitude, longitude and accuracy; missing coordinate fields are distinct from valid zero values |
| `Trip` | Resolved locations, passenger count, vehicle type, scenario, distance in km, and a sourced route summary |
| `routing.Route` | Distance in meters, nullable journey duration in seconds, source (`approximate` / `onemap`) and traffic-awareness flag |
| `routing.Router` | Injectable driving-route interface independent of ride-fare providers |
| `Quote` | Provider and product identity, category, status, currency, optional fare range and ETA, source, fetched/expiry timestamps and capacity |
| `FareRange` | `MinFare` and `MaxFare` in integer cents; equal bounds are a point fare, different bounds are a range |
| `Comparison` | Trip, collection of quotes, demo notice, collection time and expiry |
| `Service` | Encapsulates the injected providers and their timeout; coordinates comparison |
| `DemoProvider` | Holds a provider's demo configuration and implements estimation behavior |
| `ProtectedProvider` | Wraps any `Provider` with a private cache, token bucket, cooldown, in-progress lookup sharing and retry policy |
| `ProviderPolicy` | Configures request budgets, cache bounds and retry attempts for one provider |
| `ProviderHTTPError` | Carries an HTTP status and `Retry-After` from an adapter to the retry layer |

Prices use **integer cents** to avoid binary floating-point arithmetic for money. Optional fares and ETAs use pointers in Go and nullable fields in TypeScript. A missing fare is `null`; an explicitly supplied zero-price fare remains distinct and valid. Prices and ETAs must fit JavaScript's safe-integer range.

### Provider abstraction and dependency injection

```go
type Provider interface {
    ID() string
    Name() string
    Estimate(context.Context, Trip) ([]Quote, error)
}
```

The current application creates **four differently configured instances of one `DemoProvider` type**. They implement this interface and return batches of the shared `Quote` shape. Grab adds an illustrative premium tier, Zig adds an illustrative taxi range, and Ryde adds a pool tier for one or two passengers in a standard vehicle. These examples simulate fetching details; they do not fetch real app data.

A stable `(providerId, productId)` pair identifies each product. `ProductType` is a broad category, not a unique ID. A batch may contain healthy products alongside partial products, and a nonempty batch with an error preserves usable entries rather than automatically retrying the whole request. Malformed metadata and duplicate product IDs are discarded; an invalid fare or ETA can be removed while preserving the other useful field. If nothing usable remains, the provider gets an unavailable entry. `available` means fare and ETA are present, `partial` means one is missing, `unavailable` means neither can be used, and `expired` means that product's lifetime has elapsed.

`FetchedAt` is when a quote was obtained, and `ExpiresAt` is its individual validity limit. The comparison retains `collectedAt` as the oldest priced product's fetched time and `expiresAt` as the earliest expiry among priced products, including a product that expired while another adapter was running. That overall refresh hint does not invalidate longer-lived siblings: the UI checks every quote independently and removes expired quotes from recommendations. Neither cache hits nor another comparison extend a product's lifetime.

These contracts also fit a future historical-data model: `source: "model"`, product-specific ranges, and an explicit prediction lifetime. `isEstimateRange` describes unequal bounds; it does not claim a statistically calibrated confidence interval. Model versioning, feature provenance and calibration remain future work.

`main.go` wraps each demo instance using `NewProtectedProvider(provider, policy)` before injecting the wrapped list into `NewService(providers, timeout)`. Both the wrapper and underlying adapter implement `Provider`: this is the **decorator pattern through composition**. The service knows the interface rather than depending on the concrete implementation. Tests can therefore substitute a slow fake or a test-only HTTP adapter. A future permitted integration can implement the same interface with provider-specific authentication, requests and response normalization.

This uses composition and an adapter-style boundary. It does not currently contain four independently reverse-engineered provider classes.

### Separation of concerns and asynchronous programming

- **HTTP layer:** routes, JSON decoding, payload limits, validation failures and response encoding.
- **Domain layer:** location, request, trip and quote models.
- **Comparison service:** concurrency, deadlines, partial failures and result ordering.
- **Provider layer:** estimation behavior behind a shared interface.
- **Provider protection layer:** request budgets, memory cache, duplicate suppression and retry orchestration independent of fare calculations.
- **Frontend API client:** typed transport functions and error propagation.
- **UI layer:** interaction, loading states, sorting, dialogs and responsive presentation.

Other skills exercised include REST API design, JSON serialization, React hooks, dependency injection, race-aware concurrent programming, request cancellation, Git workflows and continuous integration.

### Runtime validation at the frontend boundary

`src/lib/contracts.ts` parses unknown JSON into narrowly validated UI types before state is updated. No success response is accepted merely through a TypeScript assertion. The parser checks all consumed fields, sourced route distance/duration and consistency with kilometers, nullable fares/ETAs, safe integer cents, finite coordinate ranges, recognized categories/statuses/sources, duplicate provider-product IDs, status consistency and RFC 3339 timestamp ordering. Additive unknown fields are ignored; required values are not coerced. Location catalog and current-location responses are validated too.

A malformed HTTP 200 produces an actionable invalid-response error. Malformed error bodies and non-JSON responses produce the existing unavailable-service message. The client never renders an arbitrary error object. UI helpers filter categories, handle expired products and sort ranges by their lower bound; a lowest-fare badge appears only when a product's entire range is strictly below every other fresh price in the selected category. Overlapping ranges, ties and the all-category view have no winner badge.

### Structured observability

The executable configures `slog` with a JSON handler on stderr. `provider.attempt` records each actual underlying attempt, `provider.lookup` records cache hit/miss/coalescing and total lookup latency, and `provider.result` records normalized results and invalid product counts. Fields include provider ID, duration in milliseconds, outcome, product count, error category and HTTP status when available. Logging dependencies can be injected with `WithProviderLogger` and `WithServiceLogger` for tests.

Logs omit coordinates, route IDs, cache fingerprints, credentials and arbitrary adapter error text or response bodies. Failure categories distinguish cancellation, deadlines, capacity rejection, HTTP errors and other adapter failures. These are structured logs, not a deployed metrics/tracing system.

## Coordinates and the Haversine calculation

**Haversine calculates distance between two known coordinate pairs. It does not retrieve a location or read coordinates from another app.**

`FindLocation(id)` retrieves an entry from the in-memory demo catalog. For the `current-location` ID, the backend instead validates the coordinates supplied by the browser and preserves that point without snapping it to a catalog entry. The backend converts degrees to radians and calculates the great-circle distance between pickup and destination using a spherical Earth radius of 6,371 km:

```text
a = sin²(Δlatitude / 2)
    + cos(latitude1) × cos(latitude2) × sin²(Δlongitude / 2)

distance = 2 × 6371 × atan2(√a, √(1 − a))
```

In the default offline mode, the prototype multiplies that distance by **1.25** and rounds it to one decimal place as an illustrative road-distance approximation. This factor is invented. It does not account for real roads, traffic, closures, terminal pickup arrangements or the provider's route.

Device acquisition and optional OneMap road routing are implemented; address lookup remains future work:

- **Device geolocation:** obtains the user's coordinates with permission.
- **Geocoding:** converts an address or selected place into coordinates.
- **Reverse geocoding:** converts coordinates into an address or place description.
- **Routing:** calculates a traversable road route and driving distance.

The journey summary shows numeric distance and duration with source attribution; it is not a navigation map. OneMap routing is requested once per comparison before provider fan-out. Its returned meters are divided by 1,000 without backend rounding, so every provider receives the same road distance. Display rounding does not change fare inputs. Journey duration is separate from the simulated pickup ETA. OneMap’s documented driving response does not establish live traffic awareness or guarantee the shortest route, so `trafficAware` is false.

Each endpoint has its own saved current-location snapshot. The coordinate-source IDs are `current-location` for GPS and `map-location` for a map pin; their coordinate payloads travel with the endpoint when swapping. Capturing one never overwrites the other. The interface requests GPS for pickup and lets users choose a map destination; identical coordinate pairs are rejected even if their source IDs differ. Swapping moves coordinates and saved selector options together.

With OneMap enabled, **Compare rides** sends both endpoint coordinates to OneMap. The coordinate-validation endpoint itself makes no third-party request. Coordinates are not written to disk or included in logs; the existing in-memory quote cache retains hashed trip fingerprints and quote results briefly, without raw coordinates. The interface discloses OneMap sharing before comparison. Request bodies are not placed in URL queries by the browser, but the backend-to-OneMap request follows OneMap’s documented query-parameter format.

The OneMap client has its own three-second context budget (including pacing and queueing), at most two in-flight calls and a two-calls-per-second pace per process. HTTP 429 establishes a shared `Retry-After` cooldown; later calls wait or cancel. There are no automatic routing retries and no routing-response cache. These limits do not coordinate multiple backend instances. The existing five-second provider budget begins after routing succeeds. Routing failures return 422 (unsupported/unroutable locations), 503 (token/quota), 504 (timeout) or 502 (upstream/response failures), without falling back to an approximation. Logs record routing source, latency and success without coordinates, request URLs, tokens or upstream bodies.

## Error handling and reliability

| Situation | Current behavior |
| --- | --- |
| Malformed JSON, extra fields or multiple JSON objects | HTTP 400 with an error message |
| Unknown location or identical pickup/destination | Validation failure; HTTP 400 |
| Invalid passenger count or incompatible vehicle capacity | Validation failure; the UI also selects a six-seater for more than four passengers |
| Unknown demo scenario | HTTP 400 |
| Missing or invalid device coordinates, negative accuracy, or coordinates attached to a conflicting catalog ID | HTTP 400; current-location comparisons require their coordinate payload |
| Location permission denied, lookup unavailable or timed out | Actionable message; the previous endpoint is preserved and manual selection remains available |
| Location requested from an insecure origin or unsupported browser | No device lookup is started; the UI explains the manual fallback |
| Provider returns several products or a partial response | Healthy products and useful partial fields are preserved; a whole-provider unavailable entry is used only when no valid product remains |
| Product has an invalid range or negative ETA | That field is removed; a usable sibling field can remain as partial data |
| Provider times out or is canceled | Context-aware adapter exits, its semaphore permit is released, and queued canceled calls do not start |
| API returns an error | Frontend displays the message and a retry control |
| API response cannot be parsed as JSON | Frontend displays a service-unavailable message |
| Successful HTTP response violates the runtime contract | Frontend shows an invalid-response error before updating UI state |
| Network failure | The frontend catches the rejected request and displays an error |
| Old request completes after a new request starts | Aborted request completions are ignored by the UI |
| Trip changes after comparison | Results remain associated with the previous submitted trip and an update notice appears |
| One product expires | Its card is marked expired and excluded from recommendations; later-expiring products remain usable |

The backend limits comparison bodies to 4 KB, applies server read/write timeouts, sends `Cache-Control: no-store`, and supports graceful shutdown. HTTP `no-store` prevents browser/proxy caching; it does not disable the explicit internal provider cache. Unavailable results are sorted after available prices. The outbound per-provider limits do not provide inbound abuse protection, authentication or production monitoring.

## Automated tests and continuous integration

The Go tests cover:

| Test | What it checks |
| --- | --- |
| `TestComparePartialFailureAndOrdering` | A failing Ryde adapter leaves all four providers represented, healthy products stay sorted, and the missing fare remains null |
| `TestInvalidTrips` | Rejects identical locations, five passengers in a standard vehicle, unknown locations, unexpected fields and multiple JSON objects |
| `TestTimeoutKeepsOtherProviderResult` | A timed-out provider does not discard a healthy provider's quote |
| `TestHaversineDistance` | Identical points give zero distance, one degree along the equator gives approximately 111.195 km, and distance is symmetric |
| `TestCurrentLocationValidation` | Preserves valid coordinates, accepts zero values, and rejects missing, out-of-range or non-finite values and negative accuracy |
| `TestDeviceLocationComparisonAndSwap` | Device coordinates survive validation and route swapping; conflicting IDs, missing coordinates and identical points are rejected |
| `TestCurrentLocationEndpoint` | The coordinate endpoint validates JSON, returns the device point without adding it to the shared catalog, and sets no-store |
| `TestCompareUsesActualDeviceCoordinates` | A full HTTP comparison uses the supplied coordinates and returns all four demo results |
| `TestHTTP429HonorsRetryAfterThenCachesSuccess` | A real local `httptest` server returns 429 with `Retry-After: 1`, then 200; the second request occurs at least one second later, and a repeated trip makes no third request |
| `TestHTTP429LongRetryAfterIsNotCappedToBackoff` | Integer and HTTP-date cooldowns outlast a short caller deadline, without an early retry |
| `TestHTTP429DeadlineAndCooldownProtectOtherTrips` | A 429 on the final allowed attempt still prevents another route from contacting that provider during cooldown |
| `TestHTTPRetryBudgetAndPermanentErrors` | 503 retries wait for exponential backoff and stop after three attempts; 401 is attempted once; errors are not cached |
| `TestTokenBucketBurstRefillAndProviderIsolation` / `TestEachRetryConsumesAToken` | Burst/refill behavior, cancellation, independent buckets and retries consuming tokens |
| `TestCacheKeysTTLAndCopies` / `TestCacheCapacityAndInvalidResults` | Route direction, precise coordinates, passengers, vehicle and scenario distinguish keys; expiry, LRU bounds and defensive quote copies work; invalid results are not cached |
| `TestConcurrentComparisonsCoalesceAndWaiterCanCancel` / `TestFlightCapacityAndLeaderCancellation` | Concurrent identical trips use one adapter call; cancellation and queue capacity work without keeping failed flights |
| `TestCachedComparisonKeepsOriginalFreshness` / `TestFreshThenCachedComparisonHasIdenticalExpiry` | Cached results keep their collection time and cannot extend the display expiry, including the very first refresh |
| `TestHTTPProviderCancellationReachesServer` | Parent cancellation and service deadline both cancel a real HTTP request; the fake server observes cancellation and the semaphore permit is freed |
| `TestConcurrencyLimitAcrossComparisons` / `TestCanceledQueueDoesNotStartMoreProviders` | Twelve providers across two simultaneous comparisons never exceed the configured global limit; canceled queued jobs do not call adapters |
| `TestMultipleProductsPartialDataAndErrors` / `TestQuoteNormalizationPreservesSiblings` | Multi-product responses, partial fields, partial errors, range normalization and malformed siblings do not discard healthy products |
| `TestBundleCacheHonorsShortestProductTTLAndCopies` | Whole-batch cache expires no later than the shortest product lifetime and copies nested fare/ETA values |
| `TestOneMapDrivingRouteContract` / `TestOneMapInvalidResponsesAndErrors` | Driving query/authentication and response parsing, missing fields, no route, malformed JSON, token and server failures |
| `TestOneMapCancellationReachesHTTPServer` / `TestOneMapDeadlineWhileReadingResponse` | Canceling a real outbound request is observed by the local fake server and frees the routing permit; deadlines while reading a streamed body remain timeouts |
| `TestOneMap429AppliesSharedCooldown` / `TestOneMapPacingAndCancellationBeforeRequest` / `TestOneMapConcurrencyLimitAndCanceledQueue` | Quota cooldown and request pacing block subsequent calls while respecting caller cancellation |
| `TestRoadDistanceFlowsToEveryProvider` / `TestRoutingFailureDoesNotUseApproximateFare` | A single route feeds all four providers; routing errors prevent fare evaluation instead of hiding failures behind demo distance |
| `TestInvalidTripsAndLocationCaptureDoNotCallRouter` | Validation failures and coordinate capture do not make external routing requests |
| `TestMapLocationEndpointAndComparison` / `TestInvalidMapLocationBodies` / `TestMapPointValidationAndSwap` | Confirmed map-source metadata, full coordinate precision, bounds/JSON validation, comparison use and swap behavior |
| `TestStructuredProviderLogs` | Logs contain latency, outcome and cache fields without adapter secrets or route data |

The three Python setup tests reject malformed/expired authentication responses, prevent token injection into the shell-sourced environment file, preserve unrelated settings and verify owner-only file permissions. Run `python3 -m unittest discover -s scripts/tests`; no real credentials or API calls are used.

Run all local checks:

```sh
bash scripts/check.sh
```

This runs the local Python token-setup tests, Go tests with the race detector, `go vet`, frontend location/API unit tests, TypeScript checks and a production Next.js build. GitHub Actions runs the equivalent checks on pushes and pull requests. The Go workflow checks both the supported minimum version and the current development version.

The 29 frontend tests cover synthetic geolocation and coordinate requests, runtime response validation (including malformed successful responses), category filtering, product identity, missing ETAs, range display, overlapping ranges, per-product expiry, independent endpoint capture/swapping, map bounds/precision/provenance, map-confirmation cancellation and routing contract validation. Run them with `pnpm test` from `frontend/`. They do not read anyone's real location or display a real permission prompt.

TypeScript checking and a production build are static/build checks. Domain helpers and API boundaries have behavioral unit tests, but a repeatable full browser suite remains a future improvement. Category filtering, multiple products, partial ETA display and product expiry were also checked in the local browser, along with a 390-pixel iPhone layout with no horizontal overflow. The current screenshots show the interactive destination picker and a comparison using real OneMap route distance/time with simulated fares. The map dialog and journey summary are checked at a 390-pixel iPhone width without horizontal overflow.


## Challenges and problems identified so far

### 1. Obtaining each provider's live price

The main unresolved problem is obtaining permitted, reliable live fare data for all four providers. A route-distance formula cannot reproduce demand-based prices, personal promotions, vehicle availability or provider-specific fees.

Grab publishes a [Farefeed API](https://developer.grab.com/products/farefeed-api) for selected partners, with [documented fare estimates](https://developer.grab.com/docs/partner-farefeed/). Access and Singapore coverage for this project still need confirmation. Ryde's [published merchant API](https://developer.ryde.io/) covers RydeSEND deliveries, which does not establish passenger fare access. No self-service Singapore passenger-fare API was verified for Gojek or CDG Zig during the initial research.

**Current decision:** develop the comparison flow with transparent simulated adapters, then replace them when suitable access is available. Future quote models must support fare ranges, account-specific pricing, inclusions, source and provider expiry before displaying real data.

### 2. Obtaining location and other attributes

Permission to access the user's device location is different from permission to access data inside a ride-hailing app. The prototype now supports optional device location, while provider-app data access remains unresolved.

**Use current location** invokes `navigator.geolocation.getCurrentPosition()` only after a click. The browser manages permission; an already-granted permission may not produce another prompt. The server cannot request device permission or read GPS directly. After permission succeeds, latitude, longitude and accuracy are sent in a POST body to this app's backend, which validates them and returns a `Current location` value. Raw coordinates stay in page/request memory and are not written to disk or sent to ride providers. When OneMap routing is enabled, comparing forwards both endpoint coordinates to OneMap for the route calculation. The comparison cache retains a hashed route fingerprint and a quote, without raw coordinates, in bounded process memory. Hashing is not a guarantee of anonymization. The UI preserves manual selection after errors and ignores canceled lookups.

Browser geolocation requires a secure context: localhost works for development, while iPhone access over the plain-HTTP LAN URL does not support this feature. Use HTTPS for phone geolocation; manual sample-location selection continues to work over the LAN URL. No reverse geocoding is performed, so the selected point is labelled **Current location**, rather than a street address. See [MDN's Geolocation API documentation](https://developer.mozilla.org/en-US/docs/Web/API/Geolocation_API).

Provider-specific pickup points, service names, ETAs and availability require a suitable provider integration. On iPhone, another app's private data is also protected by [iOS sandboxing](https://support.apple.com/en-au/guide/security/sec15bfe098e/web).

### 3. Respecting provider rate limits

A timeout limits how long a request takes; it does **not** limit how often requests are sent. `ProtectedProvider` now applies these rules before contacting each underlying demo adapter:

| Setting | Demo default and behavior |
| --- | --- |
| Token bucket | 2 attempts/second, burst of 2; every attempt, including retries, needs a token |
| Success cache | Complete, valid available batches only; expires at the earlier of 30 seconds from completion or the shortest product expiry; partial/error batches are not cached |
| Cache key | Directional route IDs and precise coordinates, passenger count, vehicle type, plus scenario, distance, route source and duration; route provenance prevents reusing quotes across different feature inputs; each provider has its own namespace |
| Memory bounds | 256 entries per provider with least-recently-used eviction; expired entries are removed on subsequent lookups; no disk persistence |
| Duplicate suppression | Concurrent callers for the same key share one in-progress lookup rather than spending separate tokens |
| Request capacity | At most 64 distinct in-progress cache misses per provider, including queued calls and retries; excess requests return an unavailable result |
| Retry budget | At most 3 attempts total for an empty failed batch; only typed HTTP 429, 502, 503 and 504 errors are retryable; nonempty partial/error batches are preserved without retrying |
| Backoff | Capped exponential delay of 200 ms, 400 ms, then up to 1 second, with equal jitter between half and all of each delay; three attempts require at most two retry waits |
| `Retry-After` | Integer seconds or HTTP-date; wait for at least the greater of the header delay and jittered backoff; malformed headers fall back to backoff |
| Shared cooldown | A 429, or a retryable error carrying `Retry-After`, pauses new attempts for that provider, including different routes; already-started calls cannot be recalled |
| Cancellation | Token waits, retry waits and coalesced waits honor context cancellation; the comparison's five-second budget includes all waits |

`Retry-After` is **never shortened to the backoff cap**. A provider asking for a 60-second pause causes a five-second comparison to time out rather than retry early, while the provider-wide cooldown stays in effect. A cache hit spends no token, can be served during cooldown, and preserves each quote's original `fetchedAt` and `expiresAt`; the response's `cached` field identifies actual cache reuse. Sharing an in-progress result is not marked as a cache hit.

Read the implementation in this order: `DemoProviderPolicy` and wiring in `cmd/server/main.go`; `ProtectedProvider.Estimate` for caching and coalescing; `waitForToken` for budget/cooldown; `estimate` for retry orchestration; `ProviderHTTPError` and `ParseRetryAfter` for the future HTTP adapter contract. The test-only HTTP adapter closes response bodies and uses a context-aware request against `httptest`, never a real ride service.

The first caller owns a shared lookup's context. If it cancels, waiting callers receive that cancellation and may explicitly try again; canceling only a waiting caller leaves the underlying lookup running. Cached pointer fields are copied so a caller cannot mutate the stored price. Cache expiry is enforced on read; expired entries can remain in memory until another lookup or process exit, subject to the entry bound.

These are learning defaults, **not provider-approved quotas**. Before a live integration, configure the documented budget and cache permissions, scope caches to account/currency/region where needed, respect provider quote expiry and allowed retention, and coordinate limits across multiple server processes. An actual HTTP adapter must return `ProviderHTTPError` to opt into retry behavior. The decorator does not retry bookings, discover private endpoints or bypass access controls.

### Possible estimation-first direction

[RideGuru describes](https://ride.guru/content/about/faq) an algorithm using rates, route duration/distance and other travel factors, calibrated with submitted fares. Farewise could add a separately labelled algorithmic estimate provider behind the same `Provider` interface, with approved live quotes added later. That would need sourced fare rules or a lawful calibration dataset, uncertainty ranges, and validation against held-out trips. The current Haversine road factor and invented demo constants are not a calibrated model and cannot establish the cheapest app in real time.

### 4. Platform restrictions and access controls

Private endpoints may depend on login state, expiring tokens, signed requests, certificate pinning or device checks. A captured request does not establish that it can be reused reliably or that the provider permits that use. These restrictions can also change between app releases.

**No restriction bypass, certificate-pinning bypass, credential extraction or private-endpoint scraping is implemented in this repository.** The intended integration path is a documented API, approved partner access or another explicitly permitted mechanism. Public comparison use must be checked against the applicable provider terms; being a personal, noncommercial project does not establish permission.

### 5. Making fares genuinely comparable

A useful comparison must match pickup point, passenger capacity, product type, currency, collection time and fee treatment. A fixed price, metered estimate and estimated fare range cannot be treated as identical products. Airport surcharges, ERP, platform fees, discounts and stale quotes can alter the final result.

**Current limitation:** demo totals are invented. Actual fee accounting, personal promotions and apples-to-apples product mappings remain future work.

### 6. Local development file watching

The original Next.js development watcher could not start its native macOS file-event stream in this workspace. Switching the development server to Webpack and enabling polling resolved the local startup problem. Production builds also use Webpack. This is a development-environment workaround, not a provider-data solution.

## Run locally

Install **Go 1.22+**, **Node.js 20.9+** (a supported LTS version is recommended), and **pnpm 11.25.0**.

```sh
git clone https://github.com/Raghaaavvv/farewise.git
cd farewise/frontend
pnpm install --frozen-lockfile
cd ..
bash scripts/dev.sh
```

Open **http://localhost:3000**. The script starts both servers; Ctrl+C stops them. Frontend changes reload automatically. Restart the script after editing Go code. The helper script can use the project-local runtimes on the original development machine; those runtimes are not committed and are not required when ordinary system tools are installed.

To run each component independently:

```sh
# Terminal 1, in backend/
go run ./cmd/server

# Terminal 2, in frontend/
pnpm dev
```

Open `Farewise.code-workspace` in VS Code, or open the project folder. Use **Terminal → Run Task → Start Farewise**. The Go debug configuration additionally requires the Go extension, Go on PATH and its Delve debugger. Format the frontend with `pnpm format` from `frontend/`.

Official installation references: [Go](https://go.dev/doc/install), [Node.js](https://nodejs.org/en/download), [pnpm](https://pnpm.io/installation), [Next.js](https://nextjs.org/docs/app/getting-started/installation).

### Try it on iPhone

Connect the Mac and iPhone to the same trusted Wi-Fi network. Start both servers using `bash scripts/dev.sh --hostname 0.0.0.0`, then open `http://YOUR-MAC-LAN-IP:3000` in Safari. Stop any already-running instance first. Safari's **Share → Add to Home Screen** can create an app shortcut.

This exposes the development frontend to the local network. The Go API can remain on loopback because Next.js proxies API requests. Default startup binds to loopback. Offline operation and a service worker are not implemented.

### Configuration

| Setting | Default | Purpose |
| --- | --- | --- |
| `FAREWISE_ADDR` | `127.0.0.1:8080` | Go API listening address |
| `API_BASE_URL` | `http://127.0.0.1:8080` | Next.js server-side API proxy target; set before startup |
| `FAREWISE_ROUTING_PROVIDER` | `approximate` | `approximate` uses offline demo distance; `onemap` requests Singapore driving routes |
| `ONEMAP_TOKEN` | unset | Backend-only OneMap access token, required in `onemap` mode |
| `WATCHPACK_POLLING` | `1000` during development | Polling interval in milliseconds |

No ride-provider keys are required. Routing defaults to offline mode and makes no OneMap requests. Backend startup fails clearly when `onemap` is selected without a token or a routing mode is misspelled. Never use a `NEXT_PUBLIC_` variable for tokens.

### Enable free OneMap driving routes

[OneMap’s official workshop material describes its APIs as free to use](https://www.onemap.gov.sg/apidocs/static/media/OneMap_API_Workshop_Presentation_260825.04f72136081dd249c5ee.pdf), with token-based call quotas.

1. Register through [OneMap’s API documentation](https://www.onemap.gov.sg/apidocs/). For this personal project, choose **Others**, **Website**, and describe the map selection and driving-distance use.
2. If registration provides a token, copy `backend/.env.example` to `backend/.env` (unless the local file already exists), set `FAREWISE_ROUTING_PROVIDER=onemap` and `ONEMAP_TOKEN`, and keep the file private. It is ignored by Git.
3. To generate or renew a token instead, run `python3 scripts/setup-onemap.py`. The command asks for registered credentials in your own terminal, hides the password, calls the HTTPS [authentication endpoint](https://www.onemap.gov.sg/apidocs/authentication), and atomically saves only the token to `backend/.env` with owner-only file permissions. It does not print the token or save your email/password. Other configuration fields are preserved.
4. Restart `bash scripts/dev.sh`. This script reads `backend/.env`; a direct `go run ./cmd/server` requires you to export the variables yourself.
5. Compare two different, road-accessible Singapore points. The summary should change to **Driving distance**, show journey time, and attribute OneMap / Singapore Land Authority. Simulated fares now use that road distance.

OneMap tokens expire after three days and do not automatically renew. Generate a new token, update the local file and restart when access expires. The local setup command stores only the token; account passwords are not retained and background renewal is not implemented. See the [driving-route API](https://www.onemap.gov.sg/apidocs/routing) for the request/response contract. An authenticated live sample route was verified on 14 October 2026: the Changi-to-Orchard catalog route returned 23,570 meters and 1,883 seconds. These are example responses, not guaranteed values for future queries. Both catalog and map-selected endpoints were tested through the live routing adapter; fares remained simulated. Local HTTP-server tests continue to cover error and cancellation behavior. Before reusing routing content in a future training dataset, review the chosen data source’s applicable reuse terms.

### Interactive map selection

The [official OneMap basemap example](https://www.onemap.gov.sg/docs/maps/index.html) supplies public tiles without a token. Leaflet is dynamically imported only when **Choose on map** opens. The picker preserves OneMap’s required logo and links, supports tap/drag selection and keyboard panning with **Use map center**, and validates OneMap’s published viewing bounds in both TypeScript and Go. The bounds are a viewing rectangle, not an exact national border; the routing API determines road accessibility.

Confirmation validates the coordinate pair through `POST /api/locations/map`, with source `map`, ID `map-location`, and `persisted: false`. Canceling closes the dialog without changing the trip; pending requests and map listeners are cleaned up. Coordinates are preserved at full precision and rounded only in the display. This version uses a **Pinned location** label and does not resolve a street address or implement text search. Address lookup will require a separate token-backed OneMap service.

Basemap display and road routing are separate: the map picker works without an access token, while driving distance/time requires the backend token. In approximate mode the map is real, but the journey distance is still explicitly an approximation and fares are simulated.

### Future traffic features

[LTA’s Traffic Images dataset](https://data.gov.sg/datasets/d_6cdb6b405b25aaaacbaf7689bcc6fae0/view) provides camera images and metadata. Its documented endpoint is `GET https://api.data.gov.sg/v1/transport/traffic-images`; the published API description recommends requesting once per minute, despite source retrieval every 20 seconds. A data.gov.sg key is optional for testing and gives higher limits. Camera images need further processing to become useful congestion features, and are not route travel-time measurements. This feed has not been integrated into Farewise or used to train a model.

## API contract

| Method | Endpoint | Response |
| --- | --- | --- |
| GET | `/api/health` | Service health and demo mode |
| GET | `/api/locations` | Sample locations, coordinates and `routingSource` for disclosure |
| POST | `/api/locations/current` | Validated device location, source `device`, and `persisted: false` |
| POST | `/api/locations/map` | Validated map coordinates, source `map`, ID `map-location`, and `persisted: false` |
| POST | `/api/compare` | Resolved trip, quotes, source notice and timestamps |

Example comparison request:

```json
{
  "pickupId": "changi-t3",
  "dropoffId": "orchard",
  "passengers": 1,
  "rideType": "standard",
  "scenario": "normal"
}
```

`rideType` accepts `standard` or `six`; `scenario` accepts `normal`, `busy`, `rain` or `outage`. Invalid inputs return HTTP 400 with an `error` field. Valid comparisons return HTTP 200 with a flat array containing potentially multiple products per provider. Partial products have a nullable fare or ETA; unavailable products have both null.

The internal API now replaces `amountCents` with `fare` and quote-level `collectedAt` with `fetchedAt`/`expiresAt`. Frontend and backend must be updated together. Example fare shapes in cents:

```json
{
  "pointFare": { "minFare": 1800, "maxFare": 1800, "isEstimateRange": false },
  "rangeFare": { "minFare": 2200, "maxFare": 2600, "isEstimateRange": true }
}
```

These two illustrative shapes show the value of a quote's `fare` property, not extra top-level response fields. Each quote also includes `productId`, `productType`, `fetchedAt` and `expiresAt`; the comparison's own timestamps remain separate.

For a device pickup, use `pickupId: "current-location"` and add `pickupCoordinates: { "latitude": 1.36, "longitude": 103.989, "accuracyMeters": 12 }`. These example values are synthetic. For a device drop-off, use `dropoffId: "current-location"` and `dropoffCoordinates` instead. Both endpoints may use the same reserved ID with separate, different coordinate payloads. Accuracy is optional; latitude and longitude are required. Never attach coordinates to a catalog ID.

## Project structure

```text
backend/
  cmd/server/main.go          HTTP startup, configuration and shutdown
  internal/api/               Routes, JSON handling and handler tests
  internal/fare/              Models, adapters, service and domain tests
  internal/routing/           OneMap HTTP client, pacing, cooldown and tests
  .env.example                Backend-only routing configuration template
frontend/
  src/app/                    Next.js page, layout and responsive styles
  src/components/             Icons and sourced route-distance summary
  src/lib/api.ts              Validated transport functions and fare formatting
  src/lib/contracts.ts        Response types and narrow runtime parsers
  src/lib/quote-view.ts       Category, ordering, range and expiry helpers
  src/lib/trip-location.ts    Coordinate-source snapshots, swapping and equality
  src/lib/map-selection.ts    OneMap bounds and display-only coordinate formatting
  src/lib/geolocation.ts      Browser permission and location-error handling
  tests/                      Geolocation, API contract and quote behavior tests
  next.config.ts              Same-origin proxy and watcher configuration
.github/workflows/ci.yml      Automated checks on GitHub
.vscode/                     Recommended extensions, tasks and debugger
scripts/                     Startup, toolchain lookup, local token setup and verification
docs/images/                Desktop and iPhone screenshots
Farewise.code-workspace      Portable VS Code workspace
```

## Next steps

- [ ] Confirm permitted passenger-fare access and comparison-use rights for each provider.
- [ ] Implement a live adapter, beginning with one provider, and validate it against actual app quotes.
- [x] Support batches, stable product IDs, fare ranges, partial fields and per-product expiry.
- [ ] Map actual provider fee inclusions and product names; add model/feature version provenance.
- [x] Add optional device geolocation with permission, validation, cancellation and manual fallback.
- [x] Integrate optional OneMap driving-route distance and duration with context cancellation and fake HTTP-server tests.
- [x] Configure a local OneMap token and verify authenticated catalog and map-selected routes.
- [x] Add an interactive OneMap destination picker with coordinate validation, cancellation and attribution.
- [ ] Add manual address search and geocoding.
- [ ] Add secure token renewal and a routing-cache policy after reviewing data reuse terms.
- [x] Implement provider-specific rate limiting, bounded retries and a bounded short-TTL demo cache; test 429 behavior with a fake HTTP server.
- [ ] Evaluate a calibrated algorithmic estimate provider, clearly separated from live quotes and demo prices.
- [x] Bound provider concurrency and prove context cancellation with a real fake-server HTTP call.
- [x] Add runtime API response validation and structured provider logs.
- [ ] Add full browser interaction tests.
- [ ] Add deployment configuration, authentication where needed and operational monitoring.

Farewise is a personal learning project and is not affiliated with Grab, Gojek, CDG Zig or Ryde. The repository contains a prototype, not a production ride-booking service.
