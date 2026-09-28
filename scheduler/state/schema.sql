-- Persistent state for the recurring scheduler.
-- Design points referenced throughout the code:
--   * fire identity is (schedule_id, version, kind, ordinal, fold) and UNIQUE,
--     so the same fire can only ever be materialised once (clock rollback /
--     double tick are no-ops at the DB level).
--   * editing a plan bumps schedule_version; old pending fires are cancelled,
--     confirmed history is immutable.
--   * status transitions are guarded in SQL (only the engine writes them).
--   * every state change appends a row to audit_events.

CREATE TABLE IF NOT EXISTS schedules (
    id              UUID PRIMARY KEY,
    version         INTEGER NOT NULL DEFAULT 1,
    name            TEXT NOT NULL,
    spec_json       JSONB NOT NULL,
    timezone        TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active'  -- active | paused
                      CHECK (status IN ('active','paused')),
    planned_through_local DATE,                     -- planner cursor
    created_at_utc  TIMESTAMPTZ NOT NULL,
    updated_at_utc  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS fires (
    id                  UUID PRIMARY KEY,
    schedule_id         UUID NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    version             INTEGER NOT NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('base','extra')),
    ordinal             BIGINT NOT NULL,
    fold                SMALLINT NOT NULL CHECK (fold IN (0,1)),
    fire_key            TEXT NOT NULL UNIQUE,       -- global identity
    date_local          DATE NOT NULL,
    due_utc             TIMESTAMPTZ NOT NULL,
    utc_offset_minutes  INTEGER NOT NULL,
    -- planned -> due -> succeeded | failed | expired | cancelled
    status              TEXT NOT NULL DEFAULT 'planned'
                        CHECK (status IN ('planned','due','running',
                                          'succeeded','failed','expired','cancelled')),
    run_id              UUID,                       -- set when claimed
    attempts            INTEGER NOT NULL DEFAULT 0,
    last_error_code     TEXT,
    last_error_message  TEXT,
    created_at_utc      TIMESTAMPTZ NOT NULL,
    updated_at_utc      TIMESTAMPTZ NOT NULL,
    CONSTRAINT fires_identity_unique
        UNIQUE (schedule_id, version, kind, ordinal, fold)
);
CREATE INDEX IF NOT EXISTS fires_due_idx
    ON fires (status, due_utc);
CREATE INDEX IF NOT EXISTS fires_schedule_idx
    ON fires (schedule_id, version);

-- Delivery attempts (one row per executor call): the run-level audit trail.
CREATE TABLE IF NOT EXISTS runs (
    id              UUID PRIMARY KEY,
    fire_id         UUID NOT NULL REFERENCES fires(id),
    fire_key        TEXT NOT NULL,
    attempt         INTEGER NOT NULL,
    executor_type   TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('claimed','succeeded','failed','lost')),
    claimed_at_utc  TIMESTAMPTZ NOT NULL,
    finished_at_utc TIMESTAMPTZ,
    heartbeat_at_utc TIMESTAMPTZ NOT NULL,
    error_code      TEXT,
    error_message   TEXT,
    detail_json     JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS runs_fire_idx ON runs (fire_id);
CREATE INDEX IF NOT EXISTS runs_status_idx ON runs (status, heartbeat_at_utc);

CREATE TABLE IF NOT EXISTS audit_events (
    id          BIGSERIAL PRIMARY KEY,
    ts_utc      TIMESTAMPTZ NOT NULL,
    event       TEXT NOT NULL,
    schedule_id UUID,
    fire_key    TEXT,
    run_id      UUID,
    detail_json JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS audit_schedule_idx ON audit_events (schedule_id, id);
CREATE INDEX IF NOT EXISTS audit_event_idx ON audit_events (event, id);

-- Singleton runtime watermark. last_tick_utc is monotonic: a tick with an
-- earlier 'now' is refused (clock-rollback guard), preventing re-execution.
CREATE TABLE IF NOT EXISTS runtime_state (
    singleton         INTEGER PRIMARY KEY DEFAULT 1 CHECK (singleton = 1),
    last_tick_utc     TIMESTAMPTZ,
    last_tick_result  JSONB,
    updated_at_utc    TIMESTAMPTZ NOT NULL
);
INSERT INTO runtime_state (singleton, updated_at_utc)
VALUES (1, now()) ON CONFLICT DO NOTHING;
