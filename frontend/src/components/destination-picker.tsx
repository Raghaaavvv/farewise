"use client";

import { useEffect, useRef, useState } from "react";
import type { Map as LeafletMap, Marker } from "leaflet";
import "leaflet/dist/leaflet.css";
import type { DeviceCoordinates, Location } from "@/lib/api";
import { resolveMapLocation } from "@/lib/api";
import {
  DEFAULT_MAP_CENTER,
  MAP_BOUNDS,
  mapCoordinateLabel,
  mapPoint,
} from "@/lib/map-selection";
import { Icon } from "./icon";

type Props = {
  initialLocation?: Location;
  onConfirm: (location: Location, coordinates: DeviceCoordinates) => void;
  onCancel: () => void;
};

export default function DestinationPicker({
  initialLocation,
  onConfirm,
  onCancel,
}: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const map = useRef<LeafletMap | null>(null);
  const placePin = useRef<((coordinates: DeviceCoordinates) => void) | null>(
    null,
  );
  const [ready, setReady] = useState(false);
  const [selected, setSelected] = useState<DeviceCoordinates | null>(null);
  const [error, setError] = useState("");
  const [tileError, setTileError] = useState(false);
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const request = useRef<AbortController | null>(null);
  // Initial position is a snapshot for this opening, not a moving GPS source.
  const initialCenter = useRef(
    (initialLocation &&
      mapPoint(initialLocation.latitude, initialLocation.longitude)) ||
      DEFAULT_MAP_CENTER,
  );

  useEffect(() => {
    dialog.current?.showModal();
    let alive = true;
    let instance: LeafletMap | undefined;
    void (async () => {
      try {
        const L = await import("leaflet");
        if (!alive || !canvas.current) return;
        const { south, north, west, east } = MAP_BOUNDS;
        instance = L.map(canvas.current, {
          center: [
            initialCenter.current.latitude,
            initialCenter.current.longitude,
          ],
          zoom: 14,
          minZoom: 11,
          maxZoom: 19,
          maxBounds: [
            [south, west],
            [north, east],
          ],
          maxBoundsViscosity: 1,
          scrollWheelZoom: false,
          keyboard: true,
        });
        map.current = instance;
        let pin: Marker | undefined;
        let lastPoint: DeviceCoordinates | undefined;
        const icon = L.divIcon({
          className: "destination-map-marker",
          html: '<span class="destination-pin-dot"></span>',
          iconSize: [26, 26],
          iconAnchor: [13, 13],
        });
        const select = (latitude: number, longitude: number) => {
          if (!alive || !instance || savingRef.current) return;
          const point = mapPoint(latitude, longitude);
          if (!point) {
            setError("Choose a point within the Singapore map coverage.");
            return;
          }
          setError("");
          setSelected(point);
          lastPoint = point;
          if (pin) pin.setLatLng([latitude, longitude]);
          else {
            pin = L.marker([latitude, longitude], {
              icon,
              draggable: true,
              autoPan: true,
              title: "Selected drop-off pin",
              alt: "Selected destination",
            }).addTo(instance);
            pin.bindTooltip("Drop-off", { permanent: true, direction: "top" });
            pin.on("dragend", () => {
              const position = pin!.getLatLng();
              const next = mapPoint(position.lat, position.lng);
              if (!next) {
                if (lastPoint)
                  pin!.setLatLng([lastPoint.latitude, lastPoint.longitude]);
                setError("Keep the destination within Singapore map coverage.");
                return;
              }
              select(next.latitude, next.longitude);
            });
          }
        };
        placePin.current = (point) => select(point.latitude, point.longitude);
        instance.on("click", (event: import("leaflet").LeafletMouseEvent) =>
          select(event.latlng.lat, event.latlng.lng),
        );
        const tiles = L.tileLayer(
          "https://www.onemap.gov.sg/maps/tiles/Default/{z}/{x}/{y}.png",
          {
            minZoom: 11,
            maxZoom: 19,
            bounds: [
              [south, west],
              [north, east],
            ],
            // OneMap's required attribution is kept visible in the map itself.
            attribution:
              '<img src="https://www.onemap.gov.sg/web-assets/images/logo/om_logo.png" style="height:20px;width:20px;" alt="OneMap logo"/>&nbsp;<a href="https://www.onemap.gov.sg/" target="_blank" rel="noopener noreferrer">OneMap</a>&nbsp;&copy;&nbsp;contributors&nbsp;&#124;&nbsp;<a href="https://www.sla.gov.sg/" target="_blank" rel="noopener noreferrer">Singapore Land Authority</a>',
          },
        );
        let loadedTiles = 0;
        tiles.on("tileload", () => {
          loadedTiles++;
          if (alive) setTileError(false);
        });
        tiles.on("load", () => {
          if (alive) setTileError(loadedTiles === 0);
        });
        tiles.on("tileerror", () => {
          if (alive && loadedTiles === 0) setTileError(true);
        });
        tiles.addTo(instance);
        setReady(true);
        instance.invalidateSize();
      } catch {
        if (alive)
          setError(
            "The map could not load. Close it and try again, or choose a sample destination.",
          );
      }
    })();
    return () => {
      alive = false;
      request.current?.abort();
      placePin.current = null;
      map.current = null;
      instance?.remove();
    };
  }, []);

  function useCenter() {
    if (savingRef.current) return;
    const center = map.current?.getCenter();
    if (center)
      placePin.current?.({ latitude: center.lat, longitude: center.lng });
  }

  async function confirm() {
    if (!selected || saving) return;
    const controller = new AbortController();
    request.current?.abort();
    request.current = controller;
    savingRef.current = true;
    setSaving(true);
    setError("");
    try {
      const { location } = await resolveMapLocation(
        selected,
        controller.signal,
      );
      if (!controller.signal.aborted) onConfirm(location, { ...selected });
    } catch (err) {
      if (!controller.signal.aborted)
        setError(
          err instanceof Error
            ? err.message
            : "Could not select this destination. Try again.",
        );
    } finally {
      if (!controller.signal.aborted) {
        savingRef.current = false;
        setSaving(false);
      }
    }
  }

  return (
    <dialog
      ref={dialog}
      className={`destination-picker${saving ? " map-picker-saving" : ""}`}
      aria-labelledby="map-picker-title"
      onCancel={(event) => {
        event.preventDefault();
        onCancel();
      }}
    >
      <button
        type="button"
        className="dialog-close"
        aria-label="Close destination map"
        onClick={onCancel}
      >
        <Icon name="close" />
      </button>
      <div className="eyebrow small">YOUR DESTINATION</div>
      <h2 id="map-picker-title">Pick your drop-off.</h2>
      <p className="map-instructions">
        Pan or zoom, then tap a road-accessible place to drop a pin. Drag the
        pin to adjust it.
      </p>
      <div className="destination-map-wrap">
        <div
          ref={canvas}
          className="destination-map"
          aria-label="Singapore destination map"
          tabIndex={0}
          onKeyDown={(event) => {
            if (event.code === "Space") {
              event.preventDefault();
              useCenter();
            }
          }}
        />
        {!ready && !error && (
          <p className="map-loading" role="status">
            Loading OneMap…
          </p>
        )}
      </div>
      <div className="map-selection-row">
        <div aria-live="polite">
          <strong>
            {selected
              ? "Drop-off pin selected"
              : "Tap the map to choose a place"}
          </strong>
          <span>
            {selected
              ? mapCoordinateLabel(selected)
              : "Or pan with arrow keys and select the map center."}
          </span>
        </div>
        <button
          type="button"
          className="use-location-button"
          disabled={!ready || saving}
          onClick={useCenter}
        >
          Use map center
        </button>
      </div>
      {tileError && (
        <p className="field-error" role="alert">
          Some map tiles could not load. Check your connection or use the
          sample-location dropdown.
        </p>
      )}
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <p className="map-privacy">
        No GPS permission is requested. Map tiles load from OneMap. Your
        selected coordinates are sent to this app when you confirm.
      </p>
      <div className="map-picker-actions">
        <button type="button" className="map-cancel-button" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="compare-button"
          disabled={!selected || saving}
          onClick={() => void confirm()}
        >
          {saving ? "Setting destination…" : "Use this destination"}
          <Icon name="check" size={17} />
        </button>
      </div>
    </dialog>
  );
}
