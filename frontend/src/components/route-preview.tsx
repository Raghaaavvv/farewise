import type { Route } from "@/lib/api";
import { Icon } from "./icon";

export function RoutePreview({
  route,
  loading,
  dirty,
}: {
  route?: Route;
  loading: boolean;
  dirty: boolean;
}) {
  return (
    <div
      className="route-preview route-summary"
      aria-label="Trip distance and driving time"
      aria-busy={loading}
    >
      <div className="route-summary-heading">
        <Icon name="car" size={19} />
        <strong>Journey distance</strong>
      </div>
      {loading ? (
        <p>Calculating your trip…</p>
      ) : !route ? (
        <p>Compare a trip to see its distance.</p>
      ) : (
        <>
          <div className="route-metrics">
            <div>
              <strong>{(route.distanceMeters / 1000).toFixed(1)} km</strong>
              <span>
                {route.source === "onemap"
                  ? "Driving distance"
                  : "Approximate distance"}
              </span>
            </div>
            <div>
              <strong>
                {route.durationSeconds === null
                  ? "—"
                  : `${Math.ceil(route.durationSeconds / 60)} min`}
              </strong>
              <span>Estimated driving time</span>
            </div>
          </div>
          {dirty && (
            <p className="route-stale">
              Previous trip · compare again to update.
            </p>
          )}
          {route.source === "onemap" ? (
            <p>
              Route data:{" "}
              <a
                href="https://www.onemap.gov.sg/"
                target="_blank"
                rel="noreferrer"
              >
                OneMap / Singapore Land Authority
              </a>
              . Travel time does not include waiting for pickup; live traffic is
              not included.
            </p>
          ) : (
            <p>
              Illustrative straight-line distance × 1.25. Configure OneMap for
              road routing; no driving time is estimated in demo mode.
            </p>
          )}
        </>
      )}
    </div>
  );
}
