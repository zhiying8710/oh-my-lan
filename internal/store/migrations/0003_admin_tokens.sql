-- admin_tokens 是 Web Admin UI 的长期凭证（区别于设备隧道密钥）。
-- 任意一个有效 admin_token 即可访问只读管理端点；revoke 通过 DELETE。
CREATE TABLE IF NOT EXISTS admin_tokens (
  id              TEXT PRIMARY KEY,
  token_hash      TEXT NOT NULL UNIQUE,
  label           TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  last_used_at    TEXT
);
