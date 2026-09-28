-- 周期调度服务 schema（幂等）。所有时间戳：timestamptz 存 UTC；
-- local_wall_ts 为“时区无关的本地墙钟”，真实时区在 tz_name。

CREATE TABLE IF NOT EXISTS meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);

INSERT INTO meta(key, value) VALUES ('schema_version', '1')
ON CONFLICT (key) DO NOTHING;

-- ---------------------------------------------------------------- 计划

CREATE TABLE IF NOT EXISTS schedules (
    id                     uuid PRIMARY KEY,
    name                   text NOT NULL,
    version                integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    spec                   jsonb NOT NULL,
    status                 text NOT NULL DEFAULT 'ACTIVE'
                             CHECK (status IN ('ACTIVE','PAUSED','DELETED')),
    tzdata_version         text NOT NULL,
    recovery_epoch         integer NOT NULL DEFAULT 0,
    recovery_started_at    timestamptz,
    last_healthy_tick_at   timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

-- 不可变的历史版本（每次 PUT 追加一行）
CREATE TABLE IF NOT EXISTS schedule_revisions (
    schedule_id  uuid NOT NULL REFERENCES schedules(id),
    version      integer NOT NULL,
    spec         jsonb NOT NULL,
    tzdata_version text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (schedule_id, version)
);

-- ---------------------------------------------------------------- 触发器

CREATE TABLE IF NOT EXISTS triggers (
    id                 uuid PRIMARY KEY,            -- uuid5(schedule_id, canonical name)
    schedule_id        uuid NOT NULL REFERENCES schedules(id),
    schedule_version   integer NOT NULL,
    occurrence_no      bigint NOT NULL,
    local_wall_ts      timestamp without time zone NOT NULL,
    tz_name            text NOT NULL,
    due_at             timestamptz,                 -- 真实触发 UTC 时刻；gap/missing 为 NULL
    synthetic_key      text,                        -- 'g:<wall>' / 'm:<wall>'；真实触发为 NULL
    status             text NOT NULL DEFAULT 'PENDING'
                         CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','SKIPPED','CANCELLED')),
    skip_reason        text CHECK (
                         skip_reason IS NULL OR
                         skip_reason IN ('DST_GAP','MISSING_DATE','EXCEPTION','EXPIRED','OVERFLOW','SUPERSEDED')),
    attempts           integer NOT NULL DEFAULT 0,
    not_before         timestamptz,                 -- 失败退避后的最早认领时刻
    recovery_epoch     integer NOT NULL DEFAULT 0,
    backlog_rank       bigint,
    locked_by          text,
    locked_at          timestamptz,
    claim_expires_at   timestamptz,
    run_id             uuid,
    merged_occurrences jsonb NOT NULL DEFAULT '[]'::jsonb,
    last_error         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chk_due_or_synthetic CHECK (due_at IS NOT NULL OR synthetic_key IS NOT NULL),
    CONSTRAINT chk_skip_reason_terminal CHECK (
        (status = 'SKIPPED') = (skip_reason IN ('DST_GAP','MISSING_DATE','EXPIRED','OVERFLOW'))
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_triggers_real
    ON triggers(schedule_id, due_at) WHERE due_at IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_triggers_synthetic
    ON triggers(schedule_id, synthetic_key) WHERE synthetic_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS ix_triggers_dispatch
    ON triggers(schedule_id, status, due_at);

-- ---------------------------------------------------------------- 执行尝试（审计）

CREATE TABLE IF NOT EXISTS executions (
    id           uuid PRIMARY KEY,
    trigger_id   uuid NOT NULL REFERENCES triggers(id),
    attempt_no   integer NOT NULL CHECK (attempt_no >= 1),
    worker_id    text NOT NULL,
    run_id       uuid,
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    status       text NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED','LEASE_EXPIRED')),
    code         text,
    result       text,
    error        text,
    UNIQUE (trigger_id, attempt_no)
);
CREATE INDEX IF NOT EXISTS ix_executions_trigger ON executions(trigger_id);

-- ---------------------------------------------------------------- 审计 / 运行

CREATE TABLE IF NOT EXISTS audit_events (
    id          bigserial PRIMARY KEY,
    ts          timestamptz NOT NULL DEFAULT now(),
    entity      text NOT NULL,
    entity_id   text,
    action      text NOT NULL,
    detail      jsonb NOT NULL DEFAULT '{}'::jsonb,
    run_id      uuid
);
CREATE INDEX IF NOT EXISTS ix_audit_entity ON audit_events(entity_id, ts);

CREATE TABLE IF NOT EXISTS runs (
    run_id         uuid PRIMARY KEY,
    worker_id      text NOT NULL,
    code_version   text NOT NULL,
    tzdata_version text NOT NULL,
    observed_now   timestamptz NOT NULL,
    started_at     timestamptz NOT NULL DEFAULT now(),
    ended_at       timestamptz,
    status         text NOT NULL CHECK (status IN ('OK','PARTIAL','FAILED')),
    stats          jsonb NOT NULL DEFAULT '{}'::jsonb
);

-- 每计划一行心跳，用于停机检测
CREATE TABLE IF NOT EXISTS scheduler_heartbeat (
    schedule_id  uuid PRIMARY KEY REFERENCES schedules(id),
    last_tick_at timestamptz NOT NULL
);
