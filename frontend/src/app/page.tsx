"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { compareTrip, fetchLocations, initialTrip, money } from "@/lib/api";
import type { Comparison, Location, Quote, TripRequest } from "@/lib/api";
import { Icon } from "@/components/icon";
import { RoutePreview } from "@/components/route-preview";

const scenarios: {
  id: TripRequest["scenario"];
  label: string;
  icon: "sun" | "bolt" | "rain" | "info";
}[] = [
  { id: "normal", label: "Everyday", icon: "sun" },
  { id: "busy", label: "Busy hour", icon: "bolt" },
  { id: "rain", label: "Rainy day", icon: "rain" },
  { id: "outage", label: "One unavailable", icon: "info" },
];

export default function Home() {
  const [locations, setLocations] = useState<Location[]>([]);
  const [trip, setTrip] = useState<TripRequest>(initialTrip);
  const [comparison, setComparison] = useState<Comparison | null>(null);
  const [comparedTrip, setComparedTrip] = useState<TripRequest | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [sort, setSort] = useState("price");
  const [detail, setDetail] = useState<Quote | "about" | null>(null);
  const [now, setNow] = useState(0);
  const activeRequest = useRef<AbortController | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const controller = new AbortController();
    activeRequest.current = controller;
    async function init() {
      try {
        const catalog = await fetchLocations(controller.signal);
        setLocations(catalog.locations);
        const result = await compareTrip(initialTrip, controller.signal);
        setComparison(result);
        setComparedTrip(initialTrip);
        setNow(Date.now());
      } catch (err) {
        if (!controller.signal.aborted)
          setError(
            err instanceof Error
              ? err.message
              : "Could not load your comparison.",
          );
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    }
    void init();
    const tick = setInterval(() => setNow(Date.now()), 15000);
    return () => {
      activeRequest.current?.abort();
      clearInterval(tick);
    };
  }, []);

  useEffect(() => {
    if (detail && !dialog.current?.open) dialog.current?.showModal();
    if (!detail) dialog.current?.close();
  }, [detail]);

  async function compare(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    activeRequest.current?.abort();
    const controller = new AbortController();
    activeRequest.current = controller;
    setLoading(true);
    setError("");
    const submittedTrip = { ...trip };
    try {
      if (!locations.length) {
        const catalog = await fetchLocations(controller.signal);
        setLocations(catalog.locations);
      }
      const result = await compareTrip(submittedTrip, controller.signal);
      setComparison(result);
      setComparedTrip(submittedTrip);
      setNow(Date.now());
    } catch (err) {
      if (!controller.signal.aborted)
        setError(
          err instanceof Error ? err.message : "Could not compare this trip.",
        );
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }

  const dirty =
    comparedTrip !== null &&
    JSON.stringify(trip) !== JSON.stringify(comparedTrip);
  const expired = comparison !== null && now > Date.parse(comparison.expiresAt);
  const quotes = useMemo(
    () =>
      [...(comparison?.quotes ?? [])].sort((a, b) => {
        if (a.status !== b.status) return a.status === "available" ? -1 : 1;
        const field = sort === "eta" ? "etaMinutes" : "amountCents";
        return (a[field] ?? Infinity) - (b[field] ?? Infinity);
      }),
    [comparison, sort],
  );
  const available =
    comparison?.quotes.filter(
      (q) => q.status === "available" && q.amountCents !== null,
    ) ?? [];
  const cheapest = available.length
    ? available.reduce((a, b) => (a.amountCents! < b.amountCents! ? a : b))
    : null;
  const savings =
    available.length > 1
      ? Math.max(...available.map((q) => q.amountCents!)) -
        cheapest!.amountCents!
      : 0;
  const invalid = trip.pickupId === trip.dropoffId;
  const changePassengers = (value: number) =>
    setTrip((t) => ({
      ...t,
      passengers: value,
      rideType: value > 4 ? "six" : t.rideType,
    }));

  return (
    <>
      <header className="site-header">
        <a className="wordmark" href="/" aria-label="Farewise home">
          <span className="brand-mark">
            f<span>↗</span>
          </span>
          farewise<span className="brand-dot">.</span>
        </a>
        <nav aria-label="Main navigation">
          <span className="location-tag">
            <span className="status-dot" />
            Singapore
          </span>
          <button className="text-button" onClick={() => setDetail("about")}>
            How it works <Icon name="arrow" size={16} />
          </button>
        </nav>
      </header>

      <main>
        <section className="hero" aria-labelledby="page-title">
          <div>
            <div className="eyebrow">
              <span /> YOUR WAY HOME, COMPARED
            </div>
            <h1 id="page-title">
              Four apps.
              <br />
              One <em>clear choice.</em>
            </h1>
            <p className="hero-copy">
              A little less app hopping. A little more getting home.
              <br className="desktop-break" /> Compare your ride options in one
              place.
            </p>
          </div>
          <div className="hero-note">
            <span className="note-orbit">
              <Icon name="car" size={27} />
            </span>
            <div>
              <strong>The ride is yours to choose.</strong>
              <p>
                Price, pickup time, and a clearer picture
                <br />
                before you make your move.
              </p>
            </div>
          </div>
        </section>

        <div className="demo-banner">
          <span className="demo-pill">PROTOTYPE</span>
          <p>
            You’re exploring a demo. All prices and pickup times are simulated.
          </p>
          <button
            aria-label="About simulated fares"
            onClick={() => setDetail("about")}
          >
            <Icon name="info" size={18} />
          </button>
        </div>

        <div className="comparison-layout">
          <aside className="trip-panel">
            <form onSubmit={compare}>
              <div className="panel-heading">
                <h2>Where are we heading?</h2>
                <span>01 / YOUR TRIP</span>
              </div>
              <div className="route-fields">
                <div className="route-line" aria-hidden="true">
                  <span />
                  <i />
                  <span />
                </div>
                <label className="location-field">
                  <span>PICKUP</span>
                  <select
                    aria-label="Pickup location"
                    value={trip.pickupId}
                    onChange={(e) =>
                      setTrip((t) => ({ ...t, pickupId: e.target.value }))
                    }
                    disabled={loading && !locations.length}
                  >
                    {locations.length ? (
                      locations.map((l) => (
                        <option key={l.id} value={l.id}>
                          {l.name}
                        </option>
                      ))
                    ) : (
                      <option value={trip.pickupId}>
                        Changi Airport · Terminal 3
                      </option>
                    )}
                  </select>
                </label>
                <button
                  className="swap-button"
                  type="button"
                  aria-label="Swap pickup and destination"
                  onClick={() =>
                    setTrip((t) => ({
                      ...t,
                      pickupId: t.dropoffId,
                      dropoffId: t.pickupId,
                    }))
                  }
                >
                  <Icon name="swap" size={16} />
                </button>
                <label className="location-field destination">
                  <span>DESTINATION</span>
                  <select
                    aria-label="Destination"
                    value={trip.dropoffId}
                    onChange={(e) =>
                      setTrip((t) => ({ ...t, dropoffId: e.target.value }))
                    }
                    disabled={loading && !locations.length}
                  >
                    {locations.length ? (
                      locations.map((l) => (
                        <option key={l.id} value={l.id}>
                          {l.name}
                        </option>
                      ))
                    ) : (
                      <option value={trip.dropoffId}>ION Orchard</option>
                    )}
                  </select>
                </label>
              </div>
              <div className="trip-options">
                <label>
                  <span>
                    <Icon name="people" size={16} />
                    Passengers
                  </span>
                  <select
                    aria-label="Number of passengers"
                    value={trip.passengers}
                    onChange={(e) => changePassengers(Number(e.target.value))}
                  >
                    {[1, 2, 3, 4, 5, 6].map((n) => (
                      <option value={n} key={n}>
                        {n} {n === 1 ? "person" : "people"}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <span>
                    <Icon name="car" size={16} />
                    Ride type
                  </span>
                  <select
                    aria-label="Ride type"
                    value={trip.rideType}
                    onChange={(e) =>
                      setTrip((t) => ({
                        ...t,
                        rideType: e.target.value as TripRequest["rideType"],
                      }))
                    }
                  >
                    <option value="standard" disabled={trip.passengers > 4}>
                      Standard
                    </option>
                    <option value="six">6-seater</option>
                  </select>
                </label>
              </div>
              {invalid && (
                <p className="field-error" role="alert">
                  Choose two different locations.
                </p>
              )}
              <button
                className="compare-button"
                disabled={loading || invalid}
                type="submit"
              >
                {loading ? "Comparing your options…" : "Compare rides"}
                <Icon name={loading ? "refresh" : "arrow"} size={19} />
              </button>
              <p className="form-footnote">
                <Icon name="check" size={13} /> No booking or payment required
              </p>
            </form>
            <RoutePreview distance={comparison?.trip.distanceKm} />
            <div className="catalog-note">
              <Icon name="pin" size={15} />
              <p>
                Try one of nine Singapore locations.
                <br />
                <span>Pickup points and route distance are illustrative.</span>
              </p>
            </div>
          </aside>

          <section
            className="results-panel"
            aria-labelledby="results-title"
            aria-busy={loading}
          >
            <div className="results-heading">
              <div>
                <div className="eyebrow small">02 / YOUR OPTIONS</div>
                <h2 id="results-title">Find your kind of ride.</h2>
              </div>
              <label className="sort-control">
                <span>Sort by</span>
                <select
                  aria-label="Sort rides"
                  value={sort}
                  onChange={(e) => setSort(e.target.value)}
                >
                  <option value="price">Lowest price</option>
                  <option value="eta">Soonest pickup</option>
                </select>
              </label>
            </div>

            <div className="scenario-bar">
              <span>DEMO SCENARIO</span>
              <div>
                {scenarios.map((s) => (
                  <button
                    key={s.id}
                    className={trip.scenario === s.id ? "selected" : ""}
                    aria-pressed={trip.scenario === s.id}
                    onClick={() => setTrip((t) => ({ ...t, scenario: s.id }))}
                  >
                    <Icon name={s.icon} size={14} />
                    {s.label}
                  </button>
                ))}
              </div>
            </div>

            {error && (
              <div className="error-banner" role="alert">
                <Icon name="info" />
                <div>
                  <strong>We couldn’t get your comparison.</strong>
                  <p>{error}</p>
                </div>
                <button onClick={() => void compare()} disabled={loading}>
                  Retry
                </button>
              </div>
            )}
            {dirty && !loading && (
              <div className="change-banner" role="status">
                Trip or scenario changed. Tap <strong>Compare rides</strong> to
                update the options.
              </div>
            )}

            <div className="results-summary" aria-live="polite">
              <span>
                {loading
                  ? "Finding your demo options…"
                  : comparison
                    ? `${available.length} options · ${comparison.trip.rideType === "six" ? "6-seater" : "standard"} · SGD`
                    : "Your options will appear here"}
              </span>
              {comparison && !loading && (
                <span className={expired ? "expired" : "fresh"}>
                  <span className="status-dot" />
                  {expired
                    ? "Demo quotes expired · refresh to update"
                    : `Generated ${new Date(comparison.collectedAt).toLocaleTimeString("en-SG", { hour: "2-digit", minute: "2-digit" })}`}
                </span>
              )}
            </div>

            <div className="quote-list">
              {loading
                ? [0, 1, 2, 3].map((i) => (
                    <div className="skeleton-card" key={i}>
                      <div />
                      <div />
                      <div />
                    </div>
                  ))
                : quotes.map((quote) => {
                    const best = quote.providerId === cheapest?.providerId;
                    const unavailable = quote.status !== "available";
                    return (
                      <article
                        key={quote.providerId}
                        className={`quote-card ${best ? "best" : ""} ${unavailable ? "unavailable" : ""}`}
                      >
                        <div
                          className={`provider-symbol ${quote.providerId}`}
                          aria-hidden="true"
                        >
                          {quote.providerId === "zig"
                            ? "z"
                            : quote.providerName[0].toLowerCase()}
                        </div>
                        <div className="provider-info">
                          <div className="provider-name">
                            <h3>{quote.providerName}</h3>
                            {best && (
                              <span className="best-label">
                                LOWEST DEMO FARE
                              </span>
                            )}
                          </div>
                          <p>
                            {unavailable ? quote.message : quote.serviceName}
                          </p>
                          {!unavailable && (
                            <span className="pickup-time">
                              <Icon name="clock" size={14} />
                              {quote.etaMinutes} min pickup <span>·</span> up to{" "}
                              {quote.capacity} people
                            </span>
                          )}
                        </div>
                        <div className="quote-price">
                          <strong>
                            {quote.amountCents === null
                              ? "—"
                              : money(quote.amountCents)}
                          </strong>
                          <span>
                            {unavailable ? "Unavailable" : "Simulated total"}
                          </span>
                        </div>
                        <button
                          className="detail-button"
                          aria-label={`View ${quote.providerName} quote details`}
                          onClick={() => setDetail(quote)}
                        >
                          <Icon name="chevron" size={19} />
                        </button>
                      </article>
                    );
                  })}
            </div>

            {cheapest && !loading && savings > 0 && (
              <div className="savings-note">
                <span className="savings-icon">
                  <Icon name="bolt" size={18} />
                </span>
                <p>
                  <strong>A little comparison goes a long way.</strong>
                  <br />
                  The lowest demo fare is {money(savings)} below the highest.
                </p>
                <span className="savings-amount">
                  {money(savings)}
                  <small>DEMO DIFFERENCE</small>
                </span>
              </div>
            )}
            {comparison && !loading && (
              <div className="result-footer">
                <p>
                  <Icon name="info" size={14} /> Demo totals are invented.
                  Actual fares, fees, discounts and availability may differ.
                </p>
                <button
                  className="text-button"
                  onClick={() => void compare()}
                  disabled={loading || invalid}
                >
                  <Icon name="refresh" size={14} /> Refresh comparison
                </button>
              </div>
            )}
          </section>
        </div>

        <section className="principles" aria-label="Comparison features">
          <div>
            <span>01</span>
            <h3>One trip. Four perspectives.</h3>
            <p>See your options side by side.</p>
          </div>
          <div>
            <span>02</span>
            <h3>Your priorities, your choice.</h3>
            <p>Compare price or time to pickup.</p>
          </div>
          <div>
            <span>03</span>
            <h3>A clearer way to get home.</h3>
            <p>Less switching. More deciding.</p>
          </div>
        </section>
      </main>
      <footer className="site-footer">
        <span>
          farewise. <span>A personal project, with somewhere to go.</span>
        </span>
        <span>
          Made for Singapore <span className="footer-star">✳</span>
        </span>
      </footer>

      <dialog
        ref={dialog}
        onCancel={() => setDetail(null)}
        onClose={() => setDetail(null)}
        aria-labelledby="dialog-title"
      >
        <button
          className="dialog-close"
          aria-label="Close details"
          onClick={() => setDetail(null)}
        >
          <Icon name="close" />
        </button>
        <span className="demo-pill">SIMULATED DATA</span>
        <h2 id="dialog-title">
          {detail === "about"
            ? "A clearer comparison."
            : (detail?.providerName ?? "Quote details")}
        </h2>
        {detail === "about" ? (
          <>
            <p>
              Choose a route, passenger count and ride type, then compare four
              illustrative ride options. Switch the demo scenario to explore
              busy hours, rain or an unavailable provider.
            </p>
            <p>
              Every price, pickup time and availability shown here is invented.
              This prototype does not contact Grab, Gojek, CDG Zig or Ryde, and
              it cannot book a ride.
            </p>
            <p>
              Locations are a small sample catalog. The route graphic and
              distance are illustrations, not navigation or actual driving
              directions.
            </p>
          </>
        ) : (
          detail && (
            <>
              <div className="dialog-total">
                <span>{detail.serviceName || "No quote available"}</span>
                <strong>
                  {detail.amountCents === null
                    ? "Unavailable"
                    : money(detail.amountCents)}
                </strong>
              </div>
              <p>{detail.message}</p>
              {detail.amountCents !== null && (
                <>
                  <p>{detail.feesDescription}</p>
                  <dl>
                    <div>
                      <dt>Pickup estimate</dt>
                      <dd>{detail.etaMinutes} minutes · simulated</dd>
                    </div>
                    <div>
                      <dt>Passenger capacity</dt>
                      <dd>{detail.capacity}</dd>
                    </div>
                    <div>
                      <dt>Generated</dt>
                      <dd>
                        {new Date(detail.collectedAt).toLocaleTimeString(
                          "en-SG",
                        )}
                      </dd>
                    </div>
                  </dl>
                </>
              )}
              <p className="dialog-footnote">
                Compare again after changing your trip. Confirm real fares in
                the provider’s app before booking.
              </p>
            </>
          )
        )}
        <button className="compare-button" onClick={() => setDetail(null)}>
          Got it <Icon name="check" size={17} />
        </button>
      </dialog>
    </>
  );
}
