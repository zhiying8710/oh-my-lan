-- 0007_link_health.sql
--
-- A1' 链路健康探测：server 周期 TCP-dial 每个 enabled service 的 public_port，
-- 把"最近探测成功 / 失败时间"记到 service 行。UI 在「Forward」表用这个判断
-- "链路是否真通"（mesh 的实际可用性，不止 device.last_seen_at 的 chisel 控制面心跳）。
--
-- 为什么记在 service 上而不是 forward 上：
--   - service 是 public_port 的"持有者"——同一 public_port 不会被多个 forward 抢
--   - 多条 forward 指向同一个 service 时共享同一份健康信号
--
-- 不做字节统计：chisel v1.11.6 没暴露 per-session bytes hook，daily_traffic 表
-- 留待将来 chisel 接口扩展或迁移到 wireguard 再加。
ALTER TABLE services ADD COLUMN last_probe_at DATETIME;
ALTER TABLE services ADD COLUMN last_probe_ok INTEGER NOT NULL DEFAULT 0;
