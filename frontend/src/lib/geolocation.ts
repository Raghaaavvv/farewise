// Request device coordinates only after a user action, with actionable browser errors.
import type { DeviceCoordinates } from "./api";

type LocationEnvironment = {
  secure: boolean;
  geolocation?: Pick<Geolocation, "getCurrentPosition">;
};

// Called only from a user action. Tests inject browser behavior without
// obtaining anyone's actual device coordinates or displaying a real prompt.
export function getDeviceCoordinates(
  environment?: LocationEnvironment,
): Promise<DeviceCoordinates> {
  const browserEnvironment = environment ?? {
    secure: typeof window !== "undefined" && window.isSecureContext,
    geolocation:
      typeof navigator !== "undefined" ? navigator.geolocation : undefined,
  };

  if (!browserEnvironment.secure) {
    return Promise.reject(
      new Error(
        "Location access needs HTTPS or localhost. Open this app securely, or choose a location manually.",
      ),
    );
  }

  if (!browserEnvironment.geolocation) {
    return Promise.reject(
      new Error(
        "This browser does not support location access. Choose a location manually.",
      ),
    );
  }

  return new Promise((resolve, reject) => {
    browserEnvironment.geolocation!.getCurrentPosition(
      (position) =>
        resolve({
          latitude: position.coords.latitude,
          longitude: position.coords.longitude,
          accuracyMeters: position.coords.accuracy,
        }),
      (error) => {
        const messages: Record<number, string> = {
          1: "Location permission was denied. Allow location in your browser settings to try again, or choose a location manually.",
          2: "Your device could not determine its location. Try again or choose a location manually.",
          3: "Location lookup timed out. Try again or choose a location manually.",
        };

        reject(
          new Error(
            messages[error.code] ??
              "Could not retrieve your location. Choose a location manually.",
          ),
        );
      },
      { enableHighAccuracy: true, timeout: 15000, maximumAge: 0 },
    );
  });
}
