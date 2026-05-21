-- admin_users：用户名 + argon2id 密码 hash。
-- 单用户场景：通常只插一行；schema 留多用户空间，未来要加角色/权限直接 ALTER 加列。
CREATE TABLE IF NOT EXISTS admin_users (
  id            TEXT PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,         -- argon2id 编码字符串（自带 salt + 参数）
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

-- sessions：登录后下发的 Bearer token（hash 存盘，明文仅返回一次给前端）。
-- 过期由 expires_at 控制；后台 reaper 周期清掉过期行。
CREATE TABLE IF NOT EXISTS sessions (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
  token_hash   TEXT NOT NULL UNIQUE,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_used_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
