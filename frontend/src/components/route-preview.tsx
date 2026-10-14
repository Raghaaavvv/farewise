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
  const traffic = route?.traffic;
  const trafficExpired =
    !!traffic?.expiresAt && Date.parse(traffic.expiresAt) <= Date.now();
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
              <span>
                {route.trafficAware
                  ? trafficExpired
                    ? "Previous traffic estimate"
                    : "Traffic-adjusted estimate"
                  : "Baseline driving time"}
              </span>
            </div>
          </div>
          {dirty && (
            <p className="route-stale">
              Previous trip · compare again to update.
            </p>
          )}
          {traffic && (
            <div className="traffic-summary" aria-label="LTA traffic estimate">
              {traffic.status === "applied" &&
                traffic.minSeconds !== null &&
                traffic.maxSeconds !== null && (
                  <p>
                    <strong>
                      {Math.floor(traffic.minSeconds / 60)}–
                      {Math.ceil(traffic.maxSeconds / 60)} min
                    </strong>{" "}
                    speed-band range · OneMap baseline{" "}
                    {Math.ceil(traffic.baselineSeconds / 60)} min
                  </p>
                )}
              {traffic.observedAt && (
                <p>
                  Oldest LTA observation{" "}
                  {new Intl.DateTimeFormat("en-SG", {
                    timeZone: "Asia/Singapore",
                    hour: "2-digit",
                    minute: "2-digit",
                    hour12: false,
                  }).format(new Date(traffic.observedAt))}{" "}
                  SGT · {Math.round(traffic.coverage * 100)}% of route matched.
                </p>
              )}
              <p>
                {trafficExpired
                  ? "Traffic observations expired. Compare again to refresh."
                  : traffic.message}
              </p>
            </div>
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
              {traffic && (
                <>
                  {" "}
                  · Traffic data:{" "}
                  <a
                    href="https://datamall.lta.gov.sg/"
                    target="_blank"
                    rel="noreferrer"
                  >
                    LTA DataMall
                  </a>
                </>
              )}
              . Driving time excludes waiting for pickup.
              {!route.trafficAware && " Live traffic has not been applied."}
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
