-- audit_log 记录所有改变持久状态的关键操作。
-- 作者刻意保留为单表 + 自由文本字段以便快速演进；规模上来后再考虑分表 / detail JSON 索引。
CREATE TABLE IF NOT EXISTS audit_log (
  id        TEXT PRIMARY KEY,
  ts        TEXT NOT NULL,
  actor     TEXT NOT NULL,   -- 'admin:<token_id>' / 'device:<device_id>' / 'system'
  action    TEXT NOT NULL,   -- e.g. 'device.enroll', 'device.revoke', 'service.add'
  target    TEXT,            -- 受影响实体的 id（可空）
  detail    TEXT             -- JSON 字符串，按 action 自由扩展
);

CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
