"use client";

import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import Link from "next/link";
import dynamic from "next/dynamic";
import {
  fetchLocations,
  initialTrip,
  money,
  resolveCurrentLocation,
} from "@/lib/api";
import type { Location, TripRequest } from "@/lib/api";
import { getDeviceCoordinates } from "@/lib/geolocation";
import { RoutePreview } from "@/components/route-preview";
import { Icon } from "@/components/icon";
import * as observations from "@/lib/observations";
import type {
  Fields,
  Observation,
  Page,
  Snapshot,
  Zone,
  Provider,
} from "@/lib/observations";
import { isRecord } from "@/lib/contracts";

const DestinationPicker = dynamic(
  () => import("@/components/destination-picker"),
  { ssr: false },
);
const emptyFields = {
  provider: "grab" as Provider,
  product: "",
  price: "",
  fareKind: "quoted" as Fields["fareKind"],
  quotedAt: "",
  weather: "unknown" as Fields["weather"],
  promotion: "unknown" as Fields["promotion"],
  fees: "",
  notes: "",
};
const dateLabel = (value: string) =>
  new Intl.DateTimeFormat("en-SG", {
    timeZone: "Asia/Singapore",
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));

export default function ObservationsPage() {
  const [enabled, setEnabled] = useState(false),
    [starting, setStarting] = useState(true);
  const [locations, setLocations] = useState<Location[]>([]),
    [trip, setTrip] = useState<TripRequest>(initialTrip);
  const [pickupLabel, setPickupLabel] = useState(""),
    [dropoffLabel, setDropoffLabel] = useState("");
  const [pickupZone, setPickupZone] = useState<Zone>("unknown"),
    [dropoffZone, setDropoffZone] = useState<Zone>("unknown");
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null),
    [fields, setFields] = useState(emptyFields),
    [editing, setEditing] = useState<Observation | null>(null);
  const [page, setPage] = useState<Page | null>(null),
    [providerFilter, setProviderFilter] = useState(""),
    [query, setQuery] = useState(""),
    [offset, setOffset] = useState(0);
  const [capturing, setCapturing] = useState(false),
    [saving, setSaving] = useState(false),
    [locating, setLocating] = useState(false),
    [mapOpen, setMapOpen] = useState(false),
    [reading, setReading] = useState(false);
  const [error, setError] = useState(""),
    [message, setMessage] = useState("");
  const request = useRef<AbortController | null>(null),
    readRequest = useRef<AbortController | null>(null);
  const captureAttempt = useRef<{ key: string; id: string } | null>(null),
    saveAttempt = useRef<{ key: string; id: string } | null>(null);
  const locationAttempt = useRef(0);
  const busy = capturing || saving || locating;

  useEffect(() => {
    const controller = new AbortController();
    request.current = controller;
    setFields((f) => ({ ...f, quotedAt: observations.singaporeInput() }));
    try {
      const raw = sessionStorage.getItem("farewise-observation-draft");
      sessionStorage.removeItem("farewise-observation-draft");
      if (raw) {
        const draft: unknown = JSON.parse(raw);
        if (
          isRecord(draft) &&
          isRecord(draft.trip) &&
          typeof draft.pickupLabel === "string" &&
          typeof draft.dropoffLabel === "string"
        ) {
          // This draft is only a form prefill. Go validates every field before capture.
          setTrip(draft.trip as TripRequest);
          setPickupLabel(draft.pickupLabel);
          setDropoffLabel(draft.dropoffLabel);
        }
      }
    } catch {
      /* A malformed browser draft is ignored; no observation is saved. */
    }
    void (async () => {
      try {
        const [catalog, storage] = await Promise.all([
          fetchLocations(controller.signal),
          observations.status(controller.signal),
        ]);
        if (controller.signal.aborted) return;
        setLocations(catalog.locations);
        setEnabled(storage.enabled);
        setStarting(false);
        if (storage.enabled) {
          const savedID = new URLSearchParams(window.location.search).get(
            "session",
          );
          if (savedID) {
            const saved = await observations.getSnapshot(
              savedID,
              controller.signal,
            );
            if (controller.signal.aborted) return;
            useSavedTrip(saved);
            setSnapshot(saved);
          }
          const records = await observations.list("", "", 0, controller.signal);
          if (!controller.signal.aborted) setPage(records);
        }
      } catch (err) {
        if (!controller.signal.aborted)
          setError(
            err instanceof Error ? err.message : "Could not load observations.",
          );
      } finally {
        if (!controller.signal.aborted) setStarting(false);
      }
    })();
    return () => {
      locationAttempt.current++;
      request.current?.abort();
      readRequest.current?.abort();
    };
  }, []);

  function useSavedTrip(s: Snapshot) {
    setTrip(observations.requestForSnapshot(s));
    setPickupLabel(s.trip.pickup.name);
    setDropoffLabel(s.trip.dropoff.name);
    setPickupZone(s.pickupZone);
    setDropoffZone(s.dropoffZone);
  }
  function changeTrip(next: TripRequest) {
    setTrip(next);
    setSnapshot(null);
    setMessage("");
    captureAttempt.current = null;
  }
  async function refresh(
    nextProvider = providerFilter,
    nextQuery = query,
    nextOffset = offset,
  ) {
    readRequest.current?.abort();
    const controller = new AbortController();
    readRequest.current = controller;
    setReading(true);
    try {
      const result = await observations.list(
        nextProvider,
        nextQuery,
        nextOffset,
        controller.signal,
      );
      if (!controller.signal.aborted) {
        setPage(result);
        setOffset(nextOffset);
      }
    } catch (err) {
      if (!controller.signal.aborted)
        setError(
          err instanceof Error ? err.message : "Could not load history.",
        );
    } finally {
      if (!controller.signal.aborted) setReading(false);
    }
  }
  async function captureTrip(event: FormEvent) {
    event.preventDefault();
    if (busy || editing) return;
    setError("");
    setMessage("");
    const input = { trip, pickupLabel, dropoffLabel, pickupZone, dropoffZone };
    const key = JSON.stringify(input);
    if (captureAttempt.current?.key !== key)
      captureAttempt.current = { key, id: observations.newID() };
    const controller = new AbortController();
    request.current?.abort();
    request.current = controller;
    setCapturing(true);
    try {
      const result = await observations.capture(
        { id: captureAttempt.current!.id, ...input },
        controller.signal,
      );
      if (controller.signal.aborted) return;
      setSnapshot(result);
      captureAttempt.current = null;
      saveAttempt.current = null;
      window.history.replaceState(
        null,
        "",
        `/observations?session=${result.id}`,
      );
      setMessage(
        "Trip conditions captured. Check the same trip in your provider app, then enter its fare.",
      );
    } catch (err) {
      if (!controller.signal.aborted)
        setError(
          err instanceof Error ? err.message : "Could not capture this trip.",
        );
    } finally {
      if (!controller.signal.aborted) setCapturing(false);
    }
  }
  async function locate() {
    if (busy || editing) return;
    const attempt = ++locationAttempt.current;
    setLocating(true);
    setError("");
    const controller = new AbortController();
    request.current?.abort();
    request.current = controller;
    try {
      const point = await getDeviceCoordinates();
      if (locationAttempt.current !== attempt) return;
      const result = await resolveCurrentLocation(point, controller.signal);
      if (controller.signal.aborted || locationAttempt.current !== attempt)
        return;
      changeTrip({
        ...trip,
        pickupId: result.location.id,
        pickupCoordinates: point,
      });
      setPickupLabel("Current location");
    } catch (err) {
      if (!controller.signal.aborted && locationAttempt.current === attempt)
        setError(
          err instanceof Error
            ? err.message
            : "Could not use current location.",
        );
    } finally {
      if (locationAttempt.current === attempt) setLocating(false);
    }
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!snapshot || busy) return;
    setError("");
    setMessage("");
    let input: Fields;
    try {
      input = {
        provider: fields.provider,
        product: fields.product.trim(),
        priceCents: observations.parsePrice(fields.price),
        fareKind: fields.fareKind,
        quotedAt: observations.singaporeISO(fields.quotedAt),
        weather: fields.weather,
        promotion: fields.promotion,
        includedFeesCents: fields.fees.trim()
          ? observations.parsePrice(fields.fees)
          : null,
        notes: fields.notes.trim(),
      };
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Check the observation fields.",
      );
      return;
    }
    const key = JSON.stringify({ sessionId: snapshot.id, ...input });
    if (saveAttempt.current?.key !== key)
      saveAttempt.current = { key, id: observations.newID() };
    const controller = new AbortController();
    request.current?.abort();
    request.current = controller;
    setSaving(true);
    try {
      const result = editing
        ? await observations.edit(
            editing.id,
            { ...input, revision: editing.revision },
            controller.signal,
          )
        : await observations.save(
            { id: saveAttempt.current!.id, sessionId: snapshot.id, ...input },
            controller.signal,
          );
      if (controller.signal.aborted) return;
      setMessage(
        `${editing ? "Correction saved" : "Observation saved"}.${result.contextAligned ? "" : " Its time differs from the captured conditions; review it before training."}`,
      );
      setEditing(null);
      saveAttempt.current = null;
      setFields((f) => ({
        ...f,
        price: "",
        fees: "",
        notes: "",
        quotedAt: observations.singaporeInput(),
      }));
      await refresh(providerFilter, query, 0);
    } catch (err) {
      if (!controller.signal.aborted)
        setError(
          err instanceof Error
            ? err.message
            : "Could not save this observation.",
        );
    } finally {
      if (!controller.signal.aborted) setSaving(false);
    }
  }
  function beginEdit(r: Observation) {
    setEditing(r);
    setSnapshot(r.snapshot);
    useSavedTrip(r.snapshot);
    setError("");
    setMessage(
      "Correcting a saved fare. Its captured trip conditions remain fixed.",
    );
    setFields({
      provider: r.provider,
      product: r.product,
      price: (r.priceCents / 100).toFixed(2),
      fareKind: r.fareKind,
      quotedAt: observations.singaporeInput(new Date(r.quotedAt)),
      weather: r.weather,
      promotion: r.promotion,
      fees:
        r.includedFeesCents === null
          ? ""
          : (r.includedFeesCents / 100).toFixed(2),
      notes: r.notes,
    });
    document
      .getElementById("fare-form")
      ?.scrollIntoView({ behavior: "smooth", block: "start" });
  }
  const lookup = (id: string) => locations.find((l) => l.id === id);
  const customPickup = ["current-location", "map-location"].includes(
      trip.pickupId,
    ),
    customDropoff = ["current-location", "map-location"].includes(
      trip.dropoffId,
    );
  const coordinateNote = snapshot
    ? `${dateLabel(snapshot.capturedAt)} SGT · ${snapshot.trip.route.source === "onemap" ? "OneMap driving route" : "Approximate demo route"}`
    : "";
  const timeGap =
    snapshot && fields.quotedAt
      ? new Date(`${fields.quotedAt}+08:00`).getTime() -
        Date.parse(snapshot.capturedAt)
      : 0;
  const mismatch = !!snapshot && (timeGap < -60000 || timeGap > 300000);
  const exportQuery = new URLSearchParams({
    provider: providerFilter,
    q: query,
  });

  return (
    <>
      <header className="site-header">
        <Link className="wordmark" href="/" aria-label="Farewise home">
          <span className="brand-mark">
            f<span>↗</span>
          </span>
          farewise<span className="brand-dot">.</span>
        </Link>
        <nav aria-label="Main navigation">
          <Link className="text-button" href="/">
            Compare rides
          </Link>
          <Link
            className="text-button active-nav"
            href="/observations"
            aria-current="page"
          >
            Observations
          </Link>
        </nav>
      </header>
      <main className="observations-main">
        <div className="observation-title">
          <div className="eyebrow">YOUR FARE FIELD NOTES</div>
          <h1>
            Real fares.
            <br />
            <em>Better evidence.</em>
          </h1>
          <p>
            Record the fare you see, together with the trip conditions behind
            it. Your observations stay in your local PostgreSQL database.
          </p>
        </div>
        <div className="observation-stats" aria-label="Collection summary">
          {[
            ["Observations", page?.stats.total ?? 0],
            ["Displayed quotes", page?.stats.quotes ?? 0],
            ["Paid fares", page?.stats.paid ?? 0],
            ["Distinct routes", page?.stats.routes ?? 0],
          ].map(([label, count]) => (
            <div key={label}>
              <strong>{count}</strong>
              <span>{label}</span>
            </div>
          ))}
        </div>
        {starting && (
          <p role="status">Connecting to your observation notebook…</p>
        )}
        {!starting && !enabled && (
          <div className="observation-card">
            <h2>Connect your local database.</h2>
            <p>
              Start Docker Desktop, run <code>bash scripts/db.sh up</code>, then
              restart <code>bash scripts/dev.sh</code>. Your comparison page
              stays available without observation storage.
            </p>
            <button
              className="use-location-button"
              onClick={() => window.location.reload()}
            >
              Check connection again
            </button>
          </div>
        )}
        {error && (
          <p className="observation-alert error" role="alert">
            {error}
          </p>
        )}
        {message && (
          <p className="observation-alert" role="status">
            {message}
          </p>
        )}
        {enabled && (
          <>
            <div className="observation-layout">
              <section className="observation-card">
                <div className="eyebrow small">01 / CAPTURE THE TRIP</div>
                <h2>Same trip. Same conditions.</h2>
                <p>
                  Choose the exact pickup and drop-off used in the provider app.
                  Capture before checking its fare.
                </p>
                <form onSubmit={captureTrip}>
                  <fieldset disabled={busy || !!editing}>
                    <div className="observation-label-row">
                      <label htmlFor="obs-pickup">Pickup</label>
                      <button
                        type="button"
                        className="use-location-button"
                        onClick={locate}
                      >
                        {locating ? "Locating…" : "Use current location"}
                      </button>
                    </div>
                    <select
                      id="obs-pickup"
                      value={trip.pickupId}
                      onChange={(e) => {
                        changeTrip({
                          ...trip,
                          pickupId: e.target.value,
                          pickupCoordinates: undefined,
                        });
                        setPickupLabel(lookup(e.target.value)?.name ?? "");
                      }}
                    >
                      {customPickup && (
                        <option value={trip.pickupId}>
                          {pickupLabel || "Selected coordinates"}
                        </option>
                      )}
                      {locations.map((l) => (
                        <option key={l.id} value={l.id}>
                          {l.name}
                        </option>
                      ))}
                    </select>
                    <label htmlFor="obs-pickup-label">Pickup label</label>
                    <input
                      id="obs-pickup-label"
                      maxLength={120}
                      placeholder={
                        lookup(trip.pickupId)?.name ?? "Name this pickup"
                      }
                      value={pickupLabel}
                      onChange={(e) => {
                        setPickupLabel(e.target.value);
                        setSnapshot(null);
                      }}
                    />
                    <div className="observation-label-row">
                      <label htmlFor="obs-dropoff">Drop-off</label>
                      <button
                        type="button"
                        className="use-location-button"
                        onClick={() => setMapOpen(true)}
                      >
                        Choose on map
                      </button>
                    </div>
                    <select
                      id="obs-dropoff"
                      value={trip.dropoffId}
                      onChange={(e) => {
                        changeTrip({
                          ...trip,
                          dropoffId: e.target.value,
                          dropoffCoordinates: undefined,
                        });
                        setDropoffLabel(lookup(e.target.value)?.name ?? "");
                      }}
                    >
                      {customDropoff && (
                        <option value={trip.dropoffId}>
                          {dropoffLabel || "Selected coordinates"}
                        </option>
                      )}
                      {locations.map((l) => (
                        <option key={l.id} value={l.id}>
                          {l.name}
                        </option>
                      ))}
                    </select>
                    <label htmlFor="obs-dropoff-label">Drop-off label</label>
                    <input
                      id="obs-dropoff-label"
                      maxLength={120}
                      placeholder={
                        lookup(trip.dropoffId)?.name ?? "Name this destination"
                      }
                      value={dropoffLabel}
                      onChange={(e) => {
                        setDropoffLabel(e.target.value);
                        setSnapshot(null);
                      }}
                    />
                    <div className="observation-form-grid">
                      <div>
                        <label htmlFor="obs-pickup-zone">Pickup area</label>
                        <select
                          id="obs-pickup-zone"
                          value={pickupZone}
                          onChange={(e) => {
                            setPickupZone(e.target.value as Zone);
                            setSnapshot(null);
                          }}
                        >
                          {observations.zones.map((z) => (
                            <option key={z} value={z}>
                              {z === "office" ? "Office / CBD" : z}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div>
                        <label htmlFor="obs-dropoff-zone">Drop-off area</label>
                        <select
                          id="obs-dropoff-zone"
                          value={dropoffZone}
                          onChange={(e) => {
                            setDropoffZone(e.target.value as Zone);
                            setSnapshot(null);
                          }}
                        >
                          {observations.zones.map((z) => (
                            <option key={z} value={z}>
                              {z === "office" ? "Office / CBD" : z}
                            </option>
                          ))}
                        </select>
                      </div>
                    </div>
                    <div className="observation-form-grid">
                      <div>
                        <label htmlFor="obs-passengers">Passengers</label>
                        <select
                          id="obs-passengers"
                          value={trip.passengers}
                          onChange={(e) => {
                            const n = Number(e.target.value);
                            changeTrip({
                              ...trip,
                              passengers: n,
                              rideType: n > 4 ? "six" : trip.rideType,
                            });
                          }}
                        >
                          {[1, 2, 3, 4, 5, 6].map((n) => (
                            <option key={n} value={n}>
                              {n}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div>
                        <label htmlFor="obs-vehicle">Vehicle size</label>
                        <select
                          id="obs-vehicle"
                          value={trip.rideType}
                          onChange={(e) =>
                            changeTrip({
                              ...trip,
                              rideType: e.target
                                .value as TripRequest["rideType"],
                            })
                          }
                        >
                          <option
                            value="standard"
                            disabled={trip.passengers > 4}
                          >
                            Standard
                          </option>
                          <option value="six">6-seater</option>
                        </select>
                      </div>
                    </div>
                    <button className="observation-primary" type="submit">
                      {capturing
                        ? "Capturing conditions…"
                        : snapshot
                          ? "Capture fresh conditions"
                          : "Capture trip conditions"}
                      <Icon name="arrow" />
                    </button>
                  </fieldset>
                </form>
                <p className="observation-footnote">
                  Capturing stores the selected coordinates, labels and route
                  features locally. Area types are your labels; no land-use or
                  weather API is connected.
                </p>
              </section>
              <section className="observation-card" id="fare-form">
                <div className="eyebrow small">02 / RECORD WHAT YOU SEE</div>
                <h2>
                  {editing ? "Correct this observation." : "Add a real fare."}
                </h2>
                {snapshot ? (
                  <>
                    <p className="captured-trip">
                      <strong>
                        {snapshot.trip.pickup.name} →{" "}
                        {snapshot.trip.dropoff.name}
                      </strong>
                      <span>{coordinateNote}</span>
                    </p>
                    <RoutePreview
                      route={snapshot.trip.route}
                      historical
                      loading={false}
                      dirty={false}
                    />
                  </>
                ) : (
                  <p className="observation-empty">
                    Capture a trip first. Its distance and traffic features will
                    appear here.
                  </p>
                )}
                <form onSubmit={submit}>
                  <fieldset disabled={!snapshot || busy}>
                    <div className="observation-form-grid">
                      <div>
                        <label htmlFor="obs-provider">Provider</label>
                        <select
                          id="obs-provider"
                          value={fields.provider}
                          onChange={(e) =>
                            setFields((f) => ({
                              ...f,
                              provider: e.target.value as Provider,
                            }))
                          }
                        >
                          {observations.providers.map((p) => (
                            <option key={p} value={p}>
                              {observations.providerNames[p]}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div>
                        <label htmlFor="obs-kind">Fare type</label>
                        <select
                          id="obs-kind"
                          value={fields.fareKind}
                          onChange={(e) =>
                            setFields((f) => ({
                              ...f,
                              fareKind: e.target.value as Fields["fareKind"],
                            }))
                          }
                        >
                          <option value="quoted">Displayed quote</option>
                          <option value="paid">Paid fare / receipt</option>
                        </select>
                      </div>
                    </div>
                    <label htmlFor="obs-product">Exact product name</label>
                    <input
                      id="obs-product"
                      required
                      maxLength={80}
                      placeholder="e.g. JustGrab or GrabCar"
                      value={fields.product}
                      onChange={(e) =>
                        setFields((f) => ({ ...f, product: e.target.value }))
                      }
                    />
                    <div className="observation-form-grid">
                      <div>
                        <label htmlFor="obs-price">Fare shown (SGD)</label>
                        <input
                          id="obs-price"
                          required
                          inputMode="decimal"
                          placeholder="0.00"
                          value={fields.price}
                          onChange={(e) =>
                            setFields((f) => ({ ...f, price: e.target.value }))
                          }
                        />
                      </div>
                      <div>
                        <label htmlFor="obs-fees">
                          Included fees (optional)
                        </label>
                        <input
                          id="obs-fees"
                          inputMode="decimal"
                          placeholder="Unknown"
                          value={fields.fees}
                          onChange={(e) =>
                            setFields((f) => ({ ...f, fees: e.target.value }))
                          }
                        />
                      </div>
                    </div>
                    <div className="observation-label-row">
                      <label htmlFor="obs-time">Fare observed at (SGT)</label>
                      <button
                        className="use-location-button"
                        type="button"
                        onClick={() =>
                          setFields((f) => ({
                            ...f,
                            quotedAt: observations.singaporeInput(),
                          }))
                        }
                      >
                        Use current time
                      </button>
                    </div>
                    <input
                      id="obs-time"
                      type="datetime-local"
                      step="1"
                      required
                      value={fields.quotedAt}
                      onChange={(e) =>
                        setFields((f) => ({ ...f, quotedAt: e.target.value }))
                      }
                    />
                    {mismatch && (
                      <p className="observation-warning">
                        This time does not match the captured conditions. You
                        can save it, but it will be flagged for review rather
                        than labelled ready for fare training.
                      </p>
                    )}
                    <div className="observation-form-grid">
                      <div>
                        <label htmlFor="obs-weather">Observed weather</label>
                        <select
                          id="obs-weather"
                          value={fields.weather}
                          onChange={(e) =>
                            setFields((f) => ({
                              ...f,
                              weather: e.target.value as Fields["weather"],
                            }))
                          }
                        >
                          <option value="unknown">Unknown</option>
                          <option value="dry">Dry</option>
                          <option value="rain">Rain</option>
                        </select>
                      </div>
                      <div>
                        <label htmlFor="obs-promotion">Promotion used</label>
                        <select
                          id="obs-promotion"
                          value={fields.promotion}
                          onChange={(e) =>
                            setFields((f) => ({
                              ...f,
                              promotion: e.target.value as Fields["promotion"],
                            }))
                          }
                        >
                          <option value="unknown">Unknown</option>
                          <option value="none">None</option>
                          <option value="applied">Applied</option>
                        </select>
                      </div>
                    </div>
                    <label htmlFor="obs-notes">Notes (optional)</label>
                    <textarea
                      id="obs-notes"
                      maxLength={1000}
                      rows={3}
                      placeholder="Known fees, unusual events, or context worth keeping"
                      value={fields.notes}
                      onChange={(e) =>
                        setFields((f) => ({ ...f, notes: e.target.value }))
                      }
                    />
                    <button type="submit" className="observation-primary">
                      {saving
                        ? "Saving…"
                        : editing
                          ? "Save correction"
                          : "Save observation"}
                      <Icon name="arrow" />
                    </button>
                    {editing && (
                      <button
                        type="button"
                        className="use-location-button"
                        onClick={() => {
                          setEditing(null);
                          setFields({
                            ...emptyFields,
                            quotedAt: observations.singaporeInput(),
                          });
                          setMessage("");
                        }}
                      >
                        Cancel correction
                      </button>
                    )}
                  </fieldset>
                </form>
                <p className="observation-footnote">
                  Enter the actual price shown in the provider app. Demo quotes
                  are stored separately for reference and are never treated as
                  real fares.
                </p>
              </section>
            </div>
            <section className="observation-card observation-history">
              <div className="observation-section-heading">
                <div>
                  <div className="eyebrow small">03 / YOUR COLLECTION</div>
                  <h2>Every observation counts.</h2>
                </div>
                <a
                  className="use-location-button"
                  href={`/api/observations/export?${exportQuery}`}
                >
                  Export CSV
                </a>
              </div>
              <div className="observation-coverage">
                <p>
                  <strong>Provider coverage</strong>{" "}
                  {page?.stats.byProvider.length
                    ? page.stats.byProvider
                        .map(
                          (g) =>
                            `${observations.providerNames[g.label as Provider] ?? g.label}: ${g.count}`,
                        )
                        .join(" · ")
                    : "No observations yet."}
                </p>
                <p>
                  <strong>Time coverage</strong>{" "}
                  {page?.stats.byTimeSlot.length
                    ? page.stats.byTimeSlot
                        .map((g) => `${g.label}: ${g.count}`)
                        .join(" · ")
                    : "Try repeated routes at different times."}
                </p>
                <p>
                  <strong>Weekday coverage</strong>{" "}
                  {page?.stats.byWeekday.length
                    ? page.stats.byWeekday
                        .map(
                          (g) =>
                            `${["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][Number(g.label) - 1] ?? g.label}: ${g.count}`,
                        )
                        .join(" · ")
                    : "Include weekdays and weekends."}
                </p>
                <span>
                  SGT buckets: morning 06–10 · midday 10–16 · evening 16–20 ·
                  night 20–06.
                </span>
              </div>
              <form
                className="observation-filters"
                onSubmit={(e) => {
                  e.preventDefault();
                  setError("");
                  void refresh(providerFilter, query, 0);
                }}
              >
                <label>
                  Provider
                  <select
                    value={providerFilter}
                    onChange={(e) => setProviderFilter(e.target.value)}
                  >
                    <option value="">All providers</option>
                    {observations.providers.map((p) => (
                      <option key={p} value={p}>
                        {observations.providerNames[p]}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Search
                  <input
                    value={query}
                    maxLength={120}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Route, product or notes"
                  />
                </label>
                <button
                  className="use-location-button"
                  disabled={reading}
                  type="submit"
                >
                  {reading ? "Loading…" : "Apply filters"}
                </button>
              </form>
              {!page?.records.length ? (
                <p className="observation-empty">
                  {page?.total
                    ? "No rows on this page."
                    : "No saved observations match. Your first real quote starts the collection."}
                </p>
              ) : (
                <div className="observation-table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Observed (SGT)</th>
                        <th>Provider / product</th>
                        <th>Route</th>
                        <th>Fare</th>
                        <th>Context</th>
                        <th>Action</th>
                      </tr>
                    </thead>
                    <tbody>
                      {page.records.map((r) => (
                        <tr key={r.id}>
                          <td>
                            {dateLabel(r.quotedAt)}
                            <small>
                              {r.fareKind} · {r.timeSlot}
                            </small>
                          </td>
                          <td>
                            {observations.providerNames[r.provider]}
                            <small>{r.product}</small>
                          </td>
                          <td>
                            {r.snapshot.trip.pickup.name}
                            <small>→ {r.snapshot.trip.dropoff.name}</small>
                          </td>
                          <td>{money(r.priceCents)}</td>
                          <td>
                            <span
                              className={`observation-badge${r.contextAligned ? "" : " review"}`}
                            >
                              {r.contextAligned
                                ? "Time aligned"
                                : "Review timing"}
                            </span>
                            <small>
                              {r.snapshot.trip.route.source} ·{" "}
                              {r.trafficFreshAtQuote
                                ? "fresh traffic"
                                : "baseline / no fresh traffic"}
                            </small>
                          </td>
                          <td>
                            <button
                              className="use-location-button"
                              disabled={busy}
                              onClick={() => beginEdit(r)}
                            >
                              Correct
                            </button>
                            <button
                              className="use-location-button"
                              disabled={busy}
                              onClick={() => {
                                setEditing(null);
                                useSavedTrip(r.snapshot);
                                setSnapshot(null);
                                setFields({
                                  ...emptyFields,
                                  quotedAt: observations.singaporeInput(),
                                });
                                setMessage(
                                  "Trip selected. Capture fresh conditions before recording another fare.",
                                );
                                document
                                  .getElementById("obs-pickup")
                                  ?.scrollIntoView({ behavior: "smooth" });
                              }}
                            >
                              Reuse trip
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="observation-pagination">
                <button
                  className="use-location-button"
                  disabled={reading || offset === 0}
                  onClick={() =>
                    void refresh(
                      providerFilter,
                      query,
                      Math.max(0, offset - 20),
                    )
                  }
                >
                  Previous
                </button>
                <span>
                  {page?.total
                    ? `${offset + 1}–${Math.min(offset + 20, page.total)} of ${page.total}`
                    : "0 observations"}
                </span>
                <button
                  className="use-location-button"
                  disabled={reading || offset + 20 >= (page?.total ?? 0)}
                  onClick={() =>
                    void refresh(providerFilter, query, offset + 20)
                  }
                >
                  Next
                </button>
              </div>
              <p className="observation-footnote">
                Time-aligned means capture was at most one minute after, or five
                minutes before, the fare observation. This is a
                collection-quality check, not proof of model accuracy. Paid
                fares and older entries remain available in the export with
                their quality flags.
              </p>
            </section>
          </>
        )}
      </main>
      <footer className="site-footer">
        <span className="footer-brand">farewise.</span>
        <span>Your notebook, for a clearer picture.</span>
      </footer>
      {mapOpen && (
        <DestinationPicker
          initialLocation={snapshot?.trip.dropoff ?? lookup(trip.dropoffId)}
          onCancel={() => setMapOpen(false)}
          onConfirm={(location, coordinates) => {
            changeTrip({
              ...trip,
              dropoffId: "map-location",
              dropoffCoordinates: coordinates,
            });
            setDropoffLabel(location.name);
            setMapOpen(false);
          }}
        />
      )}
    </>
  );
}
