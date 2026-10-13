# Farewise

[![CI](https://github.com/Raghaaavvv/farewise/actions/workflows/ci.yml/badge.svg)](https://github.com/Raghaaavvv/farewise/actions/workflows/ci.yml)

**A Singapore ride comparison prototype built with Go, Next.js and TypeScript.**

Farewise started with a familiar problem: after landing at the airport, finding a ride home meant switching between Grab, Gojek, CDG Zig and Ryde to compare their prices. The intended app brings comparable ride options into one screen so a user can choose based on fare, pickup time and vehicle capacity.

The current prototype demonstrates the complete frontend-to-backend comparison flow using simulated providers. **Every fare, pickup estimate and availability result is invented. No provider servers are contacted, and the app cannot book rides.** A working comparison interface does not yet mean that live fare access has been solved.

![Farewise desktop prototype showing simulated ride options](docs/images/desktop.jpg)

[View the iPhone layout](docs/images/iphone.jpg)

## What works today

- Compare four configured demo providers: Grab, Gojek, CDG Zig and Ryde.
- Choose pickup and destination from nine sample Singapore locations, or swap them.
- Use **Use current location** to request browser permission, validate the device coordinates through the Go API, and select them as the pickup. Swapping the route preserves the coordinates.
- Select one to six passengers and standard or six-seater vehicles. More than four passengers automatically selects a six-seater.
- Sort results by the lowest fare or soonest pickup.
- Explore everyday, busy-hour, rainy-day and provider-failure scenarios.
- See loading states, validation errors, retry controls, quote details and expiration notices.
- Keep healthy results visible when one provider is unavailable or times out.
- Protect each demo provider with a token bucket, a 30-second success cache, shared in-progress lookups and bounded retries.
- Use a responsive interface on desktop or iPhone.

Prices are deterministic for the same inputs. Refreshing within the 30-second cache window reuses successful quotes and their original timestamps; after cache expiry, fresh demo calculations get new timestamps. Demo quotes have a two-minute display lifetime that cache hits do not extend. All vehicles and service labels are illustrative, rather than verified provider products.

## Languages, frameworks and tools

| Technology | Role in the project |
| --- | --- |
| **Go** | Backend domain models, HTTP API, comparison service, concurrency and tests |
| **Go `net/http`** | Method-based routing, request decoding, response handling and server lifecycle; no external Go web framework |
| **Next.js 16.4** | React App Router frontend and a same-origin proxy to the Go API |
| **React** | Components, form state, asynchronous comparison state and interactive results |
| **TypeScript** | Typed requests, responses, locations, quotes and UI state |
| **HTML, CSS and SVG** | Semantic controls, responsive layouts, icons and an illustrative route graphic |
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
    E --> F[Resolve sample locations and calculate demo distance]
    F --> G[Comparison service]
    G --> H[Provider interface]
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
    I --> M[Collect results with per-provider deadlines]
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
```

The adapters run concurrently using goroutines. Each has an independent five-second total budget, including queueing, cooldowns, backoff and adapter execution, so a slow or failed adapter produces an unavailable result while the others remain usable. Real adapters will need to honor cancellation in their HTTP requests; an aggregation deadline alone cannot stop an implementation that ignores its context.

### How buttons reach the backend

| UI action | What happens |
| --- | --- |
| Initial page load | Fetches `GET /api/locations`, then sends the default trip to `POST /api/compare` |
| **Compare rides** | Prevents the normal form submission, captures the current trip, sends JSON, displays loading placeholders, then replaces results |
| **Use current location** | Calls browser geolocation after the click; sends latitude, longitude and accuracy to `POST /api/locations/current`; selects the returned location and asks the user to compare again |
| **Cancel location lookup** | Aborts the backend request if started, ignores late browser callbacks, and restores manual selection; it cannot dismiss the browser's own permission prompt |
| **Refresh comparison** or **Retry** | Calls the same comparison function with current form values; successful quotes are reused during the 30-second backend cache window |
| Change route, passengers, ride type or scenario | Updates local state; previous results remain visible with a notice until comparison is requested again |
| Change sort order | Reorders the existing response in the browser; does not make another provider request |
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
| `Trip` | Resolved location objects, passenger count, vehicle type, scenario and demo distance |
| `Quote` | Provider identity, service, status, currency, fare, ETA, source, timestamp and capacity |
| `Comparison` | Trip, collection of quotes, demo notice, collection time and expiry |
| `Service` | Encapsulates the injected providers and their timeout; coordinates comparison |
| `DemoProvider` | Holds a provider's demo configuration and implements estimation behavior |
| `ProtectedProvider` | Wraps any `Provider` with a private cache, token bucket, cooldown, in-progress lookup sharing and retry policy |
| `ProviderPolicy` | Configures request budgets, cache bounds and retry attempts for one provider |
| `ProviderHTTPError` | Carries an HTTP status and `Retry-After` from an adapter to the retry layer |

Prices use **integer cents** to avoid binary floating-point arithmetic for money. Optional amounts and ETAs use pointers in Go and `number | null` in TypeScript. A missing quote is represented as `null`, never as a zero-dollar ride.

### Provider abstraction and dependency injection

```go
type Provider interface {
    ID() string
    Name() string
    Estimate(context.Context, Trip) (Quote, error)
}
```

The current application creates **four differently configured instances of one `DemoProvider` type**. They implement this interface and return a shared `Quote` shape. They simulate fetching details; they do not fetch real app data.

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

Other skills exercised include REST API design, JSON serialization, React hooks, dependency injection, race-aware concurrent programming, request cancellation, Git workflows and continuous integration. The TypeScript types describe the expected API contract; the client currently does not perform runtime schema validation of arbitrary responses.

## Coordinates and the Haversine calculation

**Haversine calculates distance between two known coordinate pairs. It does not retrieve a location or read coordinates from another app.**

`FindLocation(id)` retrieves an entry from the in-memory demo catalog. For the `current-location` ID, the backend instead validates the coordinates supplied by the browser and preserves that point without snapping it to a catalog entry. The backend converts degrees to radians and calculates the great-circle distance between pickup and destination using a spherical Earth radius of 6,371 km:

```text
a = sin²(Δlatitude / 2)
    + cos(latitude1) × cos(latitude2) × sin²(Δlongitude / 2)

distance = 2 × 6371 × atan2(√a, √(1 − a))
```

The prototype multiplies that distance by **1.25** and rounds it to one decimal place as an illustrative road-distance approximation. This factor is invented. It does not account for real roads, traffic, closures, terminal pickup arrangements or the provider's route.

Device location acquisition is implemented; address lookup and road routing remain future work:

- **Device geolocation:** obtains the user's coordinates with permission.
- **Geocoding:** converts an address or selected place into coordinates.
- **Reverse geocoding:** converts coordinates into an address or place description.
- **Routing:** calculates a traversable road route and driving distance.

The map graphic is decorative, not a navigable map.

## Error handling and reliability

| Situation | Current behavior |
| --- | --- |
| Malformed JSON, extra fields or multiple JSON objects | HTTP 400 with an error message |
| Unknown location or identical pickup/destination | Validation failure; HTTP 400 |
| Invalid passenger count or incompatible vehicle capacity | Validation failure; the UI also selects a six-seater for more than four passengers |
| Unknown demo scenario | HTTP 400 |
| Missing or invalid device coordinates, negative accuracy, or coordinates attached to a conflicting catalog ID | HTTP 400; current-location comparisons require their coordinate payload |
| Location permission denied, lookup unavailable or timed out | Actionable message; the previous pickup is preserved and manual selection remains available |
| Location requested from an insecure origin or unsupported browser | No device lookup is started; the UI explains the manual fallback |
| Provider returns an error, times out or returns missing/invalid fare fields | An unavailable quote is returned; healthy provider quotes are preserved |
| API returns an error | Frontend displays the message and a retry control |
| API response cannot be parsed as JSON | Frontend displays a service-unavailable message |
| Network failure | The frontend catches the rejected request and displays an error |
| Old request completes after a new request starts | Aborted request completions are ignored by the UI |
| Trip changes after comparison | Results remain associated with the previous submitted trip and an update notice appears |
| Demo expiry passes | UI marks the quotes expired and offers refresh; the backend does not enforce expiry at booking because booking is not implemented |

The backend limits comparison bodies to 4 KB, applies server read/write timeouts, sends `Cache-Control: no-store`, and supports graceful shutdown. HTTP `no-store` prevents browser/proxy caching; it does not disable the explicit internal provider cache. Unavailable results are sorted after available prices. The outbound per-provider limits do not provide inbound abuse protection, authentication or production monitoring.

## Automated tests and continuous integration

The Go tests cover:

| Test | What it checks |
| --- | --- |
| `TestComparePartialFailureAndOrdering` | A failing Ryde adapter leaves all four result entries present, available fares stay sorted, and the missing fare remains null |
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

Run all local checks:

```sh
bash scripts/check.sh
```

This runs Go tests with the race detector, `go vet`, frontend location/API unit tests, TypeScript checks and a production Next.js build. GitHub Actions runs the equivalent checks on pushes and pull requests. The Go workflow checks both the supported minimum version and the current development version.

The eight frontend tests use synthetic positions and injected geolocation behavior to cover permission success, denial, unavailable positioning, timeout, insecure origins, unsupported browsers, coordinate POST requests and backend validation errors. Run them with `pnpm test` from `frontend/`. They do not read anyone's real location or display a real permission prompt.

TypeScript checking and a production build are static/build checks, not frontend behavioral tests. Sorting, the quote dialog, six-seater selection, provider-failure display and a 390-pixel mobile layout were also checked manually in the browser. Browser automation tests are a future improvement.

## Challenges and problems identified so far

### 1. Obtaining each provider's live price

The main unresolved problem is obtaining permitted, reliable live fare data for all four providers. A route-distance formula cannot reproduce demand-based prices, personal promotions, vehicle availability or provider-specific fees.

Grab publishes a [Farefeed API](https://developer.grab.com/products/farefeed-api) for selected partners, with [documented fare estimates](https://developer.grab.com/docs/partner-farefeed/). Access and Singapore coverage for this project still need confirmation. Ryde's [published merchant API](https://developer.ryde.io/) covers RydeSEND deliveries, which does not establish passenger fare access. No self-service Singapore passenger-fare API was verified for Gojek or CDG Zig during the initial research.

**Current decision:** develop the comparison flow with transparent simulated adapters, then replace them when suitable access is available. Future quote models must support fare ranges, account-specific pricing, inclusions, source and provider expiry before displaying real data.

### 2. Obtaining location and other attributes

Permission to access the user's device location is different from permission to access data inside a ride-hailing app. The prototype now supports optional device location, while provider-app data access remains unresolved.

**Use current location** invokes `navigator.geolocation.getCurrentPosition()` only after a click. The browser manages permission; an already-granted permission may not produce another prompt. The server cannot request device permission or read GPS directly. After permission succeeds, latitude, longitude and accuracy are sent in a POST body to this app's backend, which validates them and returns a `Current location` value. Raw coordinates stay in page/request memory and are not written to disk or sent to ride providers. The comparison cache retains a hashed route fingerprint and a quote, without raw coordinates, in bounded process memory. Hashing is not a guarantee of anonymization. The UI preserves manual selection after errors and ignores canceled lookups.

Browser geolocation requires a secure context: localhost works for development, while iPhone access over the plain-HTTP LAN URL does not support this feature. Use HTTPS for phone geolocation; manual sample-location selection continues to work over the LAN URL. No reverse geocoding is performed, so the selected point is labelled **Current location**, rather than a street address. See [MDN's Geolocation API documentation](https://developer.mozilla.org/en-US/docs/Web/API/Geolocation_API).

Provider-specific pickup points, service names, ETAs and availability require a suitable provider integration. On iPhone, another app's private data is also protected by [iOS sandboxing](https://support.apple.com/en-au/guide/security/sec15bfe098e/web).

### 3. Respecting provider rate limits

A timeout limits how long a request takes; it does **not** limit how often requests are sent. `ProtectedProvider` now applies these rules before contacting each underlying demo adapter:

| Setting | Demo default and behavior |
| --- | --- |
| Token bucket | 2 attempts/second, burst of 2; every attempt, including retries, needs a token |
| Success cache | 30 seconds from successful completion; available, valid quotes only |
| Cache key | Directional route IDs and precise coordinates, passenger count, vehicle type, plus scenario and distance because they affect demo prices; each provider has its own namespace |
| Memory bounds | 256 entries per provider with least-recently-used eviction; expired entries are removed on subsequent lookups; no disk persistence |
| Duplicate suppression | Concurrent callers for the same key share one in-progress lookup rather than spending separate tokens |
| Request capacity | At most 64 distinct in-progress cache misses per provider, including queued calls and retries; excess requests return an unavailable result |
| Retry budget | At most 3 attempts total; only typed HTTP 429, 502, 503 and 504 errors are retryable; unclassified errors and permanent statuses are returned immediately |
| Backoff | Capped exponential delay of 200 ms, 400 ms, then up to 1 second, with equal jitter between half and all of each delay; three attempts require at most two retry waits |
| `Retry-After` | Integer seconds or HTTP-date; wait for at least the greater of the header delay and jittered backoff; malformed headers fall back to backoff |
| Shared cooldown | A 429, or a retryable error carrying `Retry-After`, pauses new attempts for that provider, including different routes; already-started calls cannot be recalled |
| Cancellation | Token waits, retry waits and coalesced waits honor context cancellation; the comparison's five-second budget includes all waits |

`Retry-After` is **never shortened to the backoff cap**. A provider asking for a 60-second pause causes a five-second comparison to time out rather than retry early, while the provider-wide cooldown stays in effect. A cache hit spends no token, can be served during cooldown, and preserves the quote's original `collectedAt`; the response's `cached` field identifies actual cache reuse. Sharing an in-progress result is not marked as a cache hit.

Read the implementation in this order: `DemoProviderPolicy` and wiring in `cmd/server/main.go`; `ProtectedProvider.Estimate` for caching and coalescing; `waitForToken` for budget/cooldown; `estimate` for retry orchestration; `ProviderHTTPError` and `ParseRetryAfter` for the future HTTP adapter contract. The test-only HTTP adapter closes response bodies and uses a context-aware request against `httptest`, never a real ride service.

The first caller owns a shared lookup's context. If it cancels, waiting callers receive that cancellation and may explicitly try again; canceling only a waiting caller leaves the underlying lookup running. Cached pointer fields are copied so a caller cannot mutate the stored price. Cache expiry is enforced on read; expired entries can remain in memory until another lookup or process exit, subject to the entry bound.

These are learning defaults, **not provider-approved quotas**. Before a live integration, configure the documented budget and cache permissions, scope caches to account/currency/region where needed, respect provider quote expiry and allowed retention, and coordinate limits across multiple server processes. An actual HTTP adapter must return `ProviderHTTPError` to opt into retry behavior. The decorator does not retry bookings, discover private endpoints or bypass access controls.

### Possible estimation-first direction

[RideGuru describes](https://ride.guru/content/about/faq) an algorithm using rates, route duration/distance and other travel factors, calibrated with submitted fares. Farewise could add a separately labelled algorithmic estimate provider behind the same `Provider` interface, with approved live quotes added later. That would need actual road routing, sourced fare rules or a lawful calibration dataset, uncertainty ranges, and validation against held-out trips. The current Haversine road factor and invented demo constants are not a calibrated model and cannot establish the cheapest app in real time.

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
| `WATCHPACK_POLLING` | `1000` during development | Polling interval in milliseconds |

No provider keys are required or read in demo mode. Future secrets should remain in the backend environment and should never be committed or exposed through browser code.

## API contract

| Method | Endpoint | Response |
| --- | --- | --- |
| GET | `/api/health` | Service health and demo mode |
| GET | `/api/locations` | Sample locations and their coordinates |
| POST | `/api/locations/current` | Validated device location, source `device`, and `persisted: false` |
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

`rideType` accepts `standard` or `six`; `scenario` accepts `normal`, `busy`, `rain` or `outage`. Invalid inputs return HTTP 400 with an `error` field. Valid comparisons return HTTP 200, including partial failures represented as unavailable quote entries with null fare and ETA.

For a device pickup, use `pickupId: "current-location"` and add `pickupCoordinates: { "latitude": 1.36, "longitude": 103.989, "accuracyMeters": 12 }`. These example values are synthetic. After swapping, the device point uses `dropoffId: "current-location"` and `dropoffCoordinates` instead. Accuracy is optional; latitude and longitude are required. Never attach coordinates to a catalog ID.

## Project structure

```text
backend/
  cmd/server/main.go          HTTP startup, configuration and shutdown
  internal/api/               Routes, JSON handling and handler tests
  internal/fare/              Models, adapters, service and domain tests
frontend/
  src/app/                    Next.js page, layout and responsive styles
  src/components/             Icons and illustrative route graphic
  src/lib/api.ts              TypeScript contracts and transport functions
  src/lib/geolocation.ts      Browser permission and location-error handling
  tests/                      Synthetic geolocation and coordinate API unit tests
  next.config.ts              Same-origin proxy and watcher configuration
.github/workflows/ci.yml      Automated checks on GitHub
.vscode/                     Recommended extensions, tasks and debugger
scripts/                     Startup, toolchain lookup and verification
docs/images/                Desktop and iPhone screenshots
Farewise.code-workspace      Portable VS Code workspace
```

## Next steps

- [ ] Confirm permitted passenger-fare access and comparison-use rights for each provider.
- [ ] Implement a live adapter, beginning with one provider, and validate it against actual app quotes.
- [ ] Expand the quote contract to support ranges, real fee inclusions, provider expiry and source provenance.
- [x] Add optional device geolocation with permission, validation, cancellation and manual fallback.
- [ ] Add manual address search, geocoding and actual road routing.
- [x] Implement provider-specific rate limiting, bounded retries and a bounded short-TTL demo cache; test 429 behavior with a fake HTTP server.
- [ ] Evaluate a calibrated algorithmic estimate provider, clearly separated from live quotes and demo prices.
- [ ] Add browser interaction tests and runtime API response validation.
- [ ] Add deployment configuration, authentication where needed and operational monitoring.

Farewise is a personal learning project and is not affiliated with Grab, Gojek, CDG Zig or Ryde. The repository contains a prototype, not a production ride-booking service.
