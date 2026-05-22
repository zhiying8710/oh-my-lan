-- 0006_bark_settings.sql
--
-- bark 推送配置：设备 last_seen_at 超过阈值时，server 自动推送告警。
-- bark 是 iOS/macOS 上的轻量推送服务（https://github.com/Finb/Bark）；
-- server 只是个 POST 客户端，URL 由 admin 在 Web UI 上填。
--
-- Schema 设计：单行配置（id 永远 = 1）。如果未来要给每个 device / 每个 admin 用户
-- 单独配置不同的 bark URL，再拆表；当前个人单用户场景这样足够。
CREATE TABLE IF NOT EXISTS bark_settings (
    id             INTEGER PRIMARY KEY CHECK (id = 1), -- 强制单行
    enabled        INTEGER NOT NULL DEFAULT 0,         -- 0/1
    -- bark server URL 末尾要带 /<device_key>，例如:
    -- https://api.day.app/AbCdEf1234XYz
    -- server 端拼上 /title/body[/<query>] 路径完成推送。
    --
    -- 安全：bark URL 明文存储（无加密）。device_key 泄漏可被滥用给受害者手机刷推送，
    -- 影响面 = 烦人但无害（攻击者不能读你的设备数据）。依赖 SQLite 0o600 + DB 不外传作为
    -- 保护边界，与 tunnel_secret 的明文存储策略一致。
    bark_url       TEXT    NOT NULL DEFAULT '',
    -- 设备离线告警阈值（秒）；last_seen_at 超过该值视为掉线。
    -- 太小会因 30s reload 周期误报；太大错过真实故障。180 = 3 个心跳间隔。
    offline_threshold_seconds INTEGER NOT NULL DEFAULT 180,
    -- 通用提示：每个设备每次离线只推一次，避免反复轰炸（已推送记入 device_alert_state）
    updated_at     DATETIME NOT NULL
);

-- 记录每个设备"上次推过的状态"，防止反复推同一条 offline。
-- 设备从 online → offline 时插入 / 更新 alerted_at；回到 online 时删除该行。
CREATE TABLE IF NOT EXISTS device_alert_state (
    device_id   TEXT    PRIMARY KEY,
    alerted_at  DATETIME NOT NULL,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
);
