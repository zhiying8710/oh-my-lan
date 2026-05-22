# e2e smoke

本目录的脚本本地起 1 个 omlserver + 2 个 omlctl daemon（pseudo-mesh，单机多端口模拟），
跑一遍核心 mesh 场景：

  1. server 启动 → admin 登录 → 生成 enrollment token
  2. 设备 A enroll → daemon start
  3. 设备 B enroll → daemon start
  4. A 发布一个 echo service
  5. B 添加一个 forward 把 A 的 echo 映射到 B 本地端口
  6. 通过 B 的本地端口验证能拿到 echo 响应
  7. 撤销 A → forward 失活、隧道断开
  8. 全部清理

跑：

```bash
./scripts/e2e/run.sh
```

通过判定：脚本 exit 0；失败时 exit 非 0 并打印失败步骤 + 关键日志位置。

## 设计取舍

- **不进 CI**：跑一轮 ≈ 60-90 秒、用真实 TCP 监听端口、需要 server + 多 daemon
  并发 spawn，本地 dev 跑很合适但 GitHub Actions runner 起 chisel mesh 容易踩防火墙
  + race condition。CI 接入留 TODO；如果未来 v0.2 / v0.3 要 reorg chisel 协议层，
  那时再考虑容器化 + matrix。

- **单机多端口模拟，不用 docker**：项目目标读者就一个人，docker 装/启动开销远大于
  几个独立端口。脚本用 `/tmp/oml-e2e-N` 给每个组件单独 data_dir，端口固定但避开
  常用端口（19xxx）。

- **失败留现场**：脚本任一步失败不立刻 cleanup——保留 /tmp/oml-e2e/ 让人 grep
  daemon.stderr 调试。手动跑 `./scripts/e2e/clean.sh` 清掉。
