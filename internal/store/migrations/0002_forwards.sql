-- forwards 描述 "客户端 A 把远端某 service forward 到本地端口"。
-- 它跟 services 是对偶关系：services 表达"我要发布什么"，forwards 表达"我要消费什么"。
CREATE TABLE IF NOT EXISTS forwards (
  id                  TEXT PRIMARY KEY,
  owner_device_id     TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  remote_service_id   TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  local_port          INTEGER NOT NULL,
  enabled             INTEGER NOT NULL DEFAULT 1,
  created_at          TEXT NOT NULL,
  -- 同一设备本地端口不能撞
  UNIQUE(owner_device_id, local_port)
);

CREATE INDEX IF NOT EXISTS idx_forwards_owner ON forwards(owner_device_id);
CREATE INDEX IF NOT EXISTS idx_forwards_remote ON forwards(remote_service_id);
