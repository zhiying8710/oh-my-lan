-- 0008_ssh_gated_access.sql
--
-- SSH 跳板访问改造 —— 解决"暴露 RDP/SSH 到公网导致 mini-pc 被勒索"事故。
--
-- 设计见 docs/security-via-ssh-tunnel.md。要点：
--   1. service.bind_local：chisel R-listener 是否仅绑 127.0.0.1。
--      默认 1（安全），意味着任何"暴露"service 都不会被公网扫到。
--      用户在桌面客户端的 webview / 浏览器要访问，必须先 ssh -L 跳板。
--   2. devices.ssh_pubkey / ssh_username：每台 device 在 enroll 时自带 ed25519 公钥，
--      server 在 VPS 上自动建 oml-<id8> 受限账号 + 写 authorized_keys。
--   3. devices.ssh_locked_at：revoke 时 mark 时间戳，cron 7 天后才真 userdel（防误删）。
--
-- 向后兼容：本 schema 不向后兼容旧 enroll（pubkey NOT NULL）。
-- 旧 device 必须 revoke + 重新 enroll。当前数据少（≤2 台）可接受。

ALTER TABLE services ADD COLUMN bind_local INTEGER NOT NULL DEFAULT 1;

ALTER TABLE devices ADD COLUMN ssh_pubkey   TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN ssh_username TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN ssh_locked_at DATETIME;
