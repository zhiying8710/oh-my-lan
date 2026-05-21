CREATE TABLE IF NOT EXISTS devices (
  id                  TEXT PRIMARY KEY,
  name                TEXT NOT NULL UNIQUE,
  -- tunnel_secret 明文存储；功能等价于 SSH 私钥，依赖文件权限保护。
  -- 服务端重启后需把它注入 chisel UserIndex 才能让客户端继续认证。
  tunnel_secret       TEXT NOT NULL,
  status              TEXT NOT NULL DEFAULT 'offline',
  created_at          TEXT NOT NULL,
  last_seen_at        TEXT
);

CREATE TABLE IF NOT EXISTS enrollment_tokens (
  id                  TEXT PRIMARY KEY,
  token_hash          TEXT NOT NULL UNIQUE,
  expires_at          TEXT NOT NULL,
  used_at             TEXT,
  used_by_device_id   TEXT REFERENCES devices(id) ON DELETE SET NULL,
  created_at          TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS services (
  id                  TEXT PRIMARY KEY,
  device_id           TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  protocol            TEXT NOT NULL CHECK(protocol IN ('tcp','udp')),
  local_addr          TEXT NOT NULL,
  public_port         INTEGER NOT NULL UNIQUE,
  enabled             INTEGER NOT NULL DEFAULT 1,
  created_at          TEXT NOT NULL,
  UNIQUE(device_id, name)
);

CREATE INDEX IF NOT EXISTS idx_services_device ON services(device_id);
CREATE INDEX IF NOT EXISTS idx_tokens_expires ON enrollment_tokens(expires_at);
