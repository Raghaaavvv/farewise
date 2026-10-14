// Check map coordinates against the Singapore bounds before resolving a destination.
import type { DeviceCoordinates } from "./api";

// OneMap's published viewport bounds. Routing validates actual road access.
export const MAP_BOUNDS = {
  south: 1.144,
  north: 1.494,
  west: 103.535,
  east: 104.502,
};

export const DEFAULT_MAP_CENTER: DeviceCoordinates = {
  latitude: 1.304,
  longitude: 103.832,
};

/** Validate a finite map point within the bounds supported by the Singapore picker. */
export function mapPoint(
  latitude: number,
  longitude: number,
): DeviceCoordinates | null {
  if (
    !Number.isFinite(latitude) ||
    !Number.isFinite(longitude) ||
    latitude < MAP_BOUNDS.south ||
    latitude > MAP_BOUNDS.north ||
    longitude < MAP_BOUNDS.west ||
    longitude > MAP_BOUNDS.east
  ) {
    return null;
  }
  // Preserve precision for routing; round only the displayed label.
  return { latitude, longitude };
}

/** Format a map point as the coordinate label displayed beside the selected destination. */
export function mapCoordinateLabel(point: DeviceCoordinates): string {
  return `${point.latitude.toFixed(5)}, ${point.longitude.toFixed(5)}`;
}
