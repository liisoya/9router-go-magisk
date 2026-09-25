-- 9router-go 模块 · 数据库 schema 引导
-- 根因：Go 引擎没有迁移系统（DATABASE.md："auto-locates ~/.9router/db/data.sqlite"），
--       schema 历史上由 Node 版官方程序创建；全新安装的空库缺所有业务表，
--       Dashboard 会报 "no such table: settings / apiKeys"（上游 issue #20 同类问题）。
-- 修复：模块首启用 sqlite3 执行本文件（全部 IF NOT EXISTS，幂等，可每次启动跑）。
-- 来源：上游 DATABASE.md v1.9.1 官方 schema 定义。

CREATE TABLE IF NOT EXISTS providerConnections (
    id              TEXT PRIMARY KEY,
    provider        TEXT NOT NULL,
    authType        TEXT NOT NULL,
    name            TEXT,
    email           TEXT,
    priority        INTEGER,
    isActive        INTEGER DEFAULT 1,
    data            TEXT NOT NULL,
    lastUsedAt      TEXT,
    consecutiveUseCount INTEGER DEFAULT 0,
    createdAt       TEXT NOT NULL,
    updatedAt       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pc_provider ON providerConnections(provider);
CREATE INDEX IF NOT EXISTS idx_pc_provider_active ON providerConnections(provider, isActive);
CREATE INDEX IF NOT EXISTS idx_pc_priority ON providerConnections(provider, priority);

CREATE TABLE IF NOT EXISTS kv (
    scope TEXT NOT NULL,
    key   TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (scope, key)
);
CREATE INDEX IF NOT EXISTS idx_kv_scope ON kv(scope);

CREATE TABLE IF NOT EXISTS combos (
    id         TEXT PRIMARY KEY,
    name       TEXT UNIQUE NOT NULL,
    kind       TEXT,
    models     TEXT NOT NULL,
    createdAt  TEXT NOT NULL,
    updatedAt  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_combo_name ON combos(name);

CREATE TABLE IF NOT EXISTS apiKeys (
    id        TEXT PRIMARY KEY,
    key       TEXT UNIQUE NOT NULL,
    name      TEXT,
    machineId TEXT,
    isActive  INTEGER DEFAULT 1,
    createdAt TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ak_key ON apiKeys(key);

CREATE TABLE IF NOT EXISTS providerNodes (
    id        TEXT PRIMARY KEY,
    type      TEXT,
    name      TEXT,
    data      TEXT NOT NULL,
    createdAt TEXT NOT NULL,
    updatedAt TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pn_type ON providerNodes(type);

CREATE TABLE IF NOT EXISTS settings (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    data TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS usageHistory (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp        TEXT NOT NULL,
    provider         TEXT,
    model            TEXT,
    connectionId     TEXT,
    apiKey           TEXT,
    endpoint         TEXT,
    promptTokens     INTEGER DEFAULT 0,
    completionTokens INTEGER DEFAULT 0,
    cost             REAL DEFAULT 0,
    status           TEXT,
    tokens           TEXT,
    meta             TEXT
);
CREATE INDEX IF NOT EXISTS idx_uh_ts ON usageHistory(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_uh_provider ON usageHistory(provider);
CREATE INDEX IF NOT EXISTS idx_uh_model ON usageHistory(model);
CREATE INDEX IF NOT EXISTS idx_uh_conn ON usageHistory(connectionId);

CREATE TABLE IF NOT EXISTS usageDaily (
    dateKey TEXT PRIMARY KEY,
    data    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS requestDetails (
    id          TEXT PRIMARY KEY,
    timestamp   TEXT NOT NULL,
    provider    TEXT,
    model       TEXT,
    connectionId TEXT,
    status      TEXT,
    data        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rd_ts ON requestDetails(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_rd_provider ON requestDetails(provider);
CREATE INDEX IF NOT EXISTS idx_rd_model ON requestDetails(model);
CREATE INDEX IF NOT EXISTS idx_rd_conn ON requestDetails(connectionId);

CREATE TABLE IF NOT EXISTS proxyPools (
    id         TEXT PRIMARY KEY,
    isActive   INTEGER DEFAULT 1,
    testStatus TEXT,
    data       TEXT NOT NULL,
    createdAt  TEXT NOT NULL,
    updatedAt  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pp_active ON proxyPools(isActive);
CREATE INDEX IF NOT EXISTS idx_pp_status ON proxyPools(testStatus);

CREATE TABLE IF NOT EXISTS upstream_leases (
    scope      TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (scope, key)
);
