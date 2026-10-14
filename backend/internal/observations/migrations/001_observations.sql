CREATE TABLE observation_sessions (
 id uuid PRIMARY KEY,
 captured_at timestamptz NOT NULL,
 request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot)='object')
);
CREATE TABLE fare_observations (
 id uuid PRIMARY KEY,
 session_id uuid NOT NULL REFERENCES observation_sessions(id),
 request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
 provider text NOT NULL CHECK (provider IN ('grab','gojek','zig','ryde')),
 product text NOT NULL CHECK (length(product) BETWEEN 1 AND 80),
 price_cents integer NOT NULL CHECK (price_cents BETWEEN 0 AND 1000000),
 fare_kind text NOT NULL CHECK (fare_kind IN ('quoted','paid')),
 quoted_at timestamptz NOT NULL,
 weather text NOT NULL CHECK (weather IN ('unknown','dry','rain')),
 promotion text NOT NULL CHECK (promotion IN ('unknown','none','applied')),
 included_fees_cents integer CHECK (included_fees_cents BETWEEN 0 AND price_cents),
 notes text NOT NULL CHECK (length(notes)<=1000),
 revision integer NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX fare_observations_session_idx ON fare_observations(session_id);
CREATE INDEX fare_observations_quoted_idx ON fare_observations(quoted_at DESC,id);
CREATE INDEX fare_observations_provider_time_idx ON fare_observations(provider,quoted_at DESC);
