export function RoutePreview({ distance }: { distance?: number }) {
  return (
    <div
      className="route-preview"
      aria-label="Illustrative route graphic, not a navigable map"
    >
      <svg viewBox="0 0 520 250" fill="none" aria-hidden="true">
        <defs>
          <pattern
            id="streets"
            width="55"
            height="50"
            patternUnits="userSpaceOnUse"
            patternTransform="rotate(-20)"
          >
            <path d="M0 0H55V50" stroke="#fff" strokeWidth="5" />
            <path d="M25 0v50M0 25h55" stroke="#fff" strokeWidth="2" />
          </pattern>
        </defs>
        <rect width="520" height="250" fill="#eaf0e9" />
        <path
          d="M0 180c90-55 160 45 250 12s156-35 270-9v67H0z"
          fill="#dcebea"
        />
        <path
          d="M0 0h520v168c-105-15-170 30-230 25S155 140 0 180z"
          fill="url(#streets)"
        />
        <path
          d="M88 158h52l54-42h92l73-58h64"
          stroke="#d8ddd3"
          strokeWidth="13"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path
          d="M88 158h52l54-42h92l73-58h64"
          stroke="#187a64"
          strokeWidth="4"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <circle cx="88" cy="158" r="13" fill="#fff" />
        <circle cx="88" cy="158" r="7" fill="#192d29" />
        <circle cx="423" cy="58" r="13" fill="#fff" />
        <circle cx="423" cy="58" r="7" fill="#187a64" />
        <rect x="205" y="80" width="109" height="31" rx="15.5" fill="#fff" />
        <text
          x="259"
          y="100"
          textAnchor="middle"
          fill="#42635a"
          fontSize="11"
          fontFamily="sans-serif"
        >
          ROUTE PREVIEW
        </text>
        <text
          x="55"
          y="192"
          fill="#5b756b"
          fontSize="12"
          fontFamily="sans-serif"
        >
          Destination
        </text>
        <text
          x="396"
          y="91"
          fill="#5b756b"
          fontSize="12"
          fontFamily="sans-serif"
        >
          Pickup
        </text>
      </svg>
      <div className="map-caption">
        <span>Illustration · Singapore</span>
        <span>
          {distance
            ? `${distance.toFixed(1)} km · demo distance`
            : "Your way home"}
        </span>
      </div>
    </div>
  );
}
