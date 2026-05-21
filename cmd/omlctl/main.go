package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zhiying8710/oh-my-lan/internal/client"
	"github.com/zhiying8710/oh-my-lan/internal/config"
	"github.com/zhiying8710/oh-my-lan/internal/logging"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/version"
)

func main() {
	if err := newRootCmd().ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:          "omlctl",
		Short:        "oh-my-lan 客户端守护进程与控制工具",
		Long:         "oh-my-lan 客户端：维持与服务端的隧道连接，并提供命令行管理入口。",
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVarP(&configPath, "config", "c", "configs/client.example.yaml", "客户端配置文件路径")
	cmd.AddCommand(
		newEnrollCmd(&configPath),
		newStatusCmd(&configPath),
		newServiceCmd(&configPath),
		newForwardCmd(&configPath),
		newDaemonCmd(&configPath),
		newStateCmd(&configPath),
		newVersionCmd(),
	)
	return cmd
}

// ---------------- state（轻量 helper，给 Tauri 桌面 app 用）----------------

// newStateCmd 暴露最少必要的 state 元信息查询子命令。
// 桌面 app 用 `omlctl state path` 拿到 state.json 绝对路径，从而判断"是否已 enroll"，
// 这样 Rust 侧不必引入 YAML 解析依赖，行为与 daemon/enroll 走完全相同的 loadCfg。
func newStateCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "本机 state 文件辅助子命令（主要给桌面 app 调用）",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "打印 state.json 的绝对路径",
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := loadCfg(*configPath)
			if err != nil {
				return err
			}
			fmt.Println(client.StatePath(cfg.DataDir))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "exists",
		Short: "若 state 文件存在则 exit 0，否则 exit 1（不打印任何内容）",
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := loadCfg(*configPath)
			if err != nil {
				return err
			}
			p := client.StatePath(cfg.DataDir)
			if _, err := os.Stat(p); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("state 文件不存在: %s", p)
				}
				return err
			}
			return nil
		},
	})
	return cmd
}

func loadCfg(path string) (config.ClientConfig, error) {
	cfg, err := config.LoadClient(path)
	if err != nil {
		return cfg, err
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	return cfg, nil
}

func loadState(path string) (*client.State, error) {
	cfg, err := loadCfg(path)
	if err != nil {
		return nil, err
	}
	return client.LoadState(client.StatePath(cfg.DataDir))
}

// ---------------- enroll ----------------

func newEnrollCmd(configPath *string) *cobra.Command {
	var serverURL, token, deviceName string
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "使用一次性 token 把当前设备注册到服务端",
		RunE: func(cmd *cobra.Command, args []string) error {
			if serverURL == "" || token == "" || deviceName == "" {
				return fmt.Errorf("--server, --token, --name 都必填")
			}
			cfg, err := loadCfg(*configPath)
			if err != nil {
				return err
			}
			statePath := client.StatePath(cfg.DataDir)
			state, err := client.EnrollNew(cmd.Context(), serverURL, token, deviceName, statePath)
			if err != nil {
				return err
			}
			fmt.Printf("注册成功：device_id=%s name=%s\n", state.DeviceID, state.DeviceName)
			fmt.Printf("chisel 入口=%s  fingerprint=%s\n", state.ChiselAddr, state.ServerFingerprint)
			fmt.Printf("state 文件已写入：%s\n", statePath)
			return nil
		},
	}
	cmd.Flags().StringVar(&serverURL, "server", "", "服务端控制平面 URL，例如 http://vps:8080")
	cmd.Flags().StringVar(&token, "token", "", "一次性 enrollment token (ot_xxx)")
	cmd.Flags().StringVar(&deviceName, "name", "", "本设备名（在服务端唯一）")
	return cmd
}

// ---------------- status ----------------

func newStatusCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看当前设备身份与已发布服务",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			fmt.Printf("server_url=%s\ndevice_id=%s\ndevice_name=%s\nchisel_addr=%s\nfingerprint=%s\n",
				state.ServerURL, state.DeviceID, state.DeviceName, state.ChiselAddr, state.ServerFingerprint)
			api := client.EnrolledAPIClient(state)
			list, err := api.ListServices(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("\n已发布服务 (%d)：\n", len(list))
			printServices(list)
			return nil
		},
	}
}

// ---------------- service ----------------

func newServiceCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "管理本机要发布的服务",
	}
	cmd.AddCommand(
		newServiceAddCmd(configPath),
		newServiceListCmd(configPath),
		newServiceRmCmd(configPath),
		newServiceEnableCmd(configPath, true),
		newServiceEnableCmd(configPath, false),
	)
	return cmd
}

func newServiceEnableCmd(configPath *string, enable bool) *cobra.Command {
	use, short, action := "enable <id>", "启用服务", "已启用"
	if !enable {
		use, short, action = "disable <id>", "停用服务", "已停用"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			var svc proto.ServiceDTO
			if enable {
				svc, err = api.EnableService(cmd.Context(), args[0])
			} else {
				svc, err = api.DisableService(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			fmt.Printf("%s：%s（id=%s, enabled=%v）\n", action, svc.Name, svc.ID, svc.Enabled)
			fmt.Println("⚠️  daemon 会在最多 30 秒内自动 reload 隧道使变更生效")
			return nil
		},
	}
}

func newServiceAddCmd(configPath *string) *cobra.Command {
	var name, protocol, local string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "发布一个本地 TCP/UDP 服务到公网",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || local == "" {
				return fmt.Errorf("--name 和 --local 必填")
			}
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			svc, err := api.AddService(cmd.Context(), proto.AddServiceRequest{
				Name: name, Protocol: protocol, LocalAddr: local,
			})
			if err != nil {
				return err
			}
			fmt.Printf("服务已发布：%s（id=%s）\n  公网端口=%d  协议=%s  本地=%s\n",
				svc.Name, svc.ID, svc.PublicPort, svc.Protocol, svc.LocalAddr)
			fmt.Println("⚠️  M1 阶段：新增服务需要重启 daemon 才会建立隧道（M2 改为热生效）")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "服务名（同一设备内唯一）")
	cmd.Flags().StringVar(&protocol, "proto", "tcp", "tcp / udp")
	cmd.Flags().StringVar(&local, "local", "", "本地目标地址：仅端口号(默认 127.0.0.1)，如 22；或完整 host:port，如 192.168.1.10:22")
	return cmd
}

func newServiceListCmd(configPath *string) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出本设备已发布的服务（--all 显示所有设备）",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			if all {
				list, err := api.ListAllServices(cmd.Context())
				if err != nil {
					return err
				}
				printAllServices(list)
				return nil
			}
			list, err := api.ListServices(cmd.Context())
			if err != nil {
				return err
			}
			printServices(list)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "列出所有设备的服务，便于挑选 forward 目标")
	return cmd
}

func newServiceRmCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <service-id>",
		Short: "删除一个已发布的服务",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			if err := api.DeleteService(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Println("已删除")
			return nil
		},
	}
}

// ---------------- forward ----------------

func newForwardCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forward",
		Short: "管理本机要 forward 到的远端服务（mesh）",
		Long:  "forward 用于把另一台设备发布的服务桥接到本机端口；daemon 会自动 reload 生效。",
	}
	cmd.AddCommand(
		newForwardAddCmd(configPath),
		newForwardListCmd(configPath),
		newForwardRmCmd(configPath),
		newForwardToggleCmd(configPath, true),
		newForwardToggleCmd(configPath, false),
	)
	return cmd
}

func newForwardToggleCmd(configPath *string, enable bool) *cobra.Command {
	use, short, action := "enable <id>", "启用 forward", "已启用"
	if !enable {
		use, short, action = "disable <id>", "停用 forward", "已停用"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			var f proto.ForwardDTO
			if enable {
				f, err = api.EnableForward(cmd.Context(), args[0])
			} else {
				f, err = api.DisableForward(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			fmt.Printf("%s：local_port=%d → %s（id=%s, enabled=%v）\n",
				action, f.LocalPort, f.RemoteServiceName, f.ID, f.Enabled)
			fmt.Println("⚠️  daemon 会在最多一个 reload 周期内自动调整 L: 隧道")
			return nil
		},
	}
}

func newForwardAddCmd(configPath *string) *cobra.Command {
	var service string
	var localPort int
	cmd := &cobra.Command{
		Use:   "add",
		Short: "把指定 service forward 到本机端口",
		RunE: func(cmd *cobra.Command, args []string) error {
			if service == "" || localPort <= 0 {
				return fmt.Errorf("--service 和 --local 必填")
			}
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			f, err := api.AddForward(cmd.Context(), proto.AddForwardRequest{
				RemoteServiceID: service,
				LocalPort:       localPort,
			})
			if err != nil {
				return err
			}
			fmt.Printf("forward 已创建（id=%s）\n", f.ID)
			fmt.Printf("  本机 127.0.0.1:%d  ->  服务 %s（device=%s, public_port=%d, proto=%s）\n",
				f.LocalPort, f.RemoteServiceName, f.RemoteDeviceID, f.RemotePublicPort, f.Protocol)
			fmt.Println("⚠️  daemon 会在最多一个 reload 周期内自动建立 L: forward 隧道")
			return nil
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "远端 service id（可用 service list --all 查看）")
	cmd.Flags().IntVar(&localPort, "local", 0, "本机要监听的端口（127.0.0.1）")
	return cmd
}

func newForwardListCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出本设备的 forward 规则",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			list, err := api.ListForwards(cmd.Context())
			if err != nil {
				return err
			}
			printForwards(list)
			return nil
		},
	}
}

func newForwardRmCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <forward-id>",
		Short: "删除一条 forward 规则",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadState(*configPath)
			if err != nil {
				return err
			}
			api := client.EnrolledAPIClient(state)
			if err := api.DeleteForward(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Println("已删除")
			return nil
		},
	}
}

// ---------------- daemon ----------------

func newDaemonCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "控制本机 daemon 进程",
	}
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "前台启动 daemon，维持隧道连接",
	}
	// --pid-file 让外层（Tauri 桌面 app / launchd / systemd）能用一份文件统一探活。
	// 不指定时不写文件，保持 CLI 友好。
	var pidFile string
	startCmd.Flags().StringVar(&pidFile, "pid-file", "", "启动后将当前 pid 写入该文件，退出时尝试删除（用于外部探活）")
	startCmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := loadCfg(*configPath)
		if err != nil {
			return err
		}
		state, err := client.LoadState(client.StatePath(cfg.DataDir))
		if err != nil {
			return err
		}
		logger := logging.New(logging.Options{Level: cfg.Log.Level, Format: cfg.Log.Format})

		// 写 pidfile：用 atomic rename，避免 reader 读到半行。
		// 写之前先 preempt：若 pidfile 里指向一个活的 omlctl 进程，先把它 SIGTERM 掉再写。
		// 解决场景：用户手动 Start → 又开启开机自启，launchd 拉起的新 omlctl 接管 pidfile，
		// 旧的 omlctl 就变孤儿；preempt 让新实例正式接管，旧的实例先退出。
		if pidFile != "" {
			preemptOldDaemon(pidFile, logger)
			if err := writePidFile(pidFile); err != nil {
				return fmt.Errorf("写 pid 文件 %s: %w", pidFile, err)
			}
			defer func() {
				if rmErr := os.Remove(pidFile); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
					logger.Warn("清理 pid 文件失败", "path", pidFile, "err", rmErr)
				}
			}()
		}

		daemon := client.NewDaemon(state, logger)
		if cfg.Log.Level == "debug" {
			daemon.SetVerbose(true)
		}
		if cfg.ReloadIntervalSeconds > 0 {
			daemon.SetReloadInterval(time.Duration(cfg.ReloadIntervalSeconds) * time.Second)
		}

		sigCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		logger.Info("daemon 启动",
			"version", version.Version,
			"device", state.DeviceName,
			"server", state.ServerURL,
			"pid_file", pidFile,
		)
		err = daemon.Run(sigCtx)
		if err != nil && err != context.Canceled {
			return err
		}
		logger.Info("daemon 已停止")
		return nil
	}
	cmd.AddCommand(startCmd)

	// daemon kill：兜底清理同一份 config 下的所有 omlctl daemon 进程。
	// 用于 Tauri 关闭开机自启时调用，能扫掉 pidfile 找不到的孤儿。
	killCmd := &cobra.Command{
		Use:   "kill",
		Short: "杀掉所有当前 config 下的 omlctl daemon 进程（孤儿清理用）",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadCfg(*configPath)
			if err != nil {
				return err
			}
			pidFile := filepath.Join(cfg.DataDir, "..", "daemon.pid")
			// 1) 先杀 pidfile 中的 pid（如有）
			if raw, err := os.ReadFile(pidFile); err == nil {
				if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil && pid > 0 {
					sigTermAndWait(pid, 3*time.Second)
				}
				_ = os.Remove(pidFile)
			}
			// 2) ps-grep 兜底找孤儿——按命令行里出现的 config 路径精确匹配，避免误杀
			killed := killOmlctlByConfig(*configPath)
			fmt.Printf("已尝试清理 omlctl daemon 进程；ps-grep 命中 %d 个\n", killed)
			return nil
		},
	}
	cmd.AddCommand(killCmd)
	return cmd
}

// preemptOldDaemon 在新 daemon 写 pidfile 前，先把 pidfile 中那个活着的 omlctl 进程接管掉。
// 安全约束：用 ps 验证目标进程命令行里出现 "omlctl"，避免 pid 复用时误杀其它进程。
func preemptOldDaemon(pidFile string, logger *slog.Logger) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	oldPid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || oldPid <= 0 || oldPid == os.Getpid() {
		return
	}
	if !processIsAlive(oldPid) {
		return
	}
	if !processLooksLikeOmlctl(oldPid) {
		logger.Warn("pidfile 中的 pid 不像 omlctl，跳过抢占以免误杀", "pid", oldPid)
		return
	}
	logger.Warn("发现旧 omlctl daemon，发送 SIGTERM 接管", "pid", oldPid)
	if !sigTermAndWait(oldPid, 5*time.Second) {
		logger.Warn("旧 daemon SIGTERM 5s 未生效，SIGKILL", "pid", oldPid)
		_ = syscall.Kill(oldPid, syscall.SIGKILL)
		time.Sleep(200 * time.Millisecond)
	}
}

func processIsAlive(pid int) bool {
	// signal=0 不发信号，仅做权限+存活检测
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM = 存在但无权限；ESRCH = 不存在
	return errors.Is(err, syscall.EPERM)
}

// 用 ps 看进程命令行里是否包含 "omlctl"。失败回退到 false（保守，宁可不杀也别误杀）。
func processLooksLikeOmlctl(pid int) bool {
	// `ps -p PID -o command=` 在 macOS / Linux 都支持
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "omlctl")
}

// sigTermAndWait 向 pid 发 SIGTERM，等待 timeout 期间反复检查是否退出。
// 返回 true 表示 timeout 内已退出。不返回 SIGKILL 后的结果，让调用方按需升级。
func sigTermAndWait(pid int, timeout time.Duration) bool {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return !processIsAlive(pid)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if !processIsAlive(pid) {
			return true
		}
	}
	return false
}

// killOmlctlByConfig 通过 ps 扫描所有进程，命令行包含 `omlctl` 和给定 config 路径
// 的全部 SIGTERM 掉。**自身 pid 排除在外**。返回命中并尝试 kill 的数量。
//
// 实现要点：先把所有命中的 pid 收齐并 SIGTERM，再用 WaitGroup 并行等 SIGKILL 升级。
// 历史教训：一开始 SIGKILL 升级放在裸 goroutine 里，main 不等就 return，omlctl 进程退出
// 把 goroutine 也带走，结果 SIGKILL 永远不发——hang 在 chisel client 的 omlctl 不会被清掉。
func killOmlctlByConfig(configPath string) int {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	out, err := exec.Command("ps", "-eo", "pid,command").Output()
	if err != nil {
		return 0
	}
	self := os.Getpid()
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == self || pid <= 1 {
			continue
		}
		cmd := strings.Join(fields[1:], " ")
		if !strings.Contains(cmd, "omlctl") || !strings.Contains(cmd, "daemon start") {
			continue
		}
		if !strings.Contains(cmd, abs) && !strings.Contains(cmd, configPath) {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
		pids = append(pids, pid)
	}
	if len(pids) == 0 {
		return 0
	}
	// 并行等待 + 必要时 SIGKILL；main 阻塞到所有都处理完
	var wg sync.WaitGroup
	for _, pid := range pids {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if !processIsAlive(p) {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			_ = syscall.Kill(p, syscall.SIGKILL)
		}(pid)
	}
	wg.Wait()
	return len(pids)
}

// writePidFile 原子写入当前 pid。先写 tmp 再 rename，避免读端读到半行。
func writePidFile(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	body := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------- version ----------------

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.String())
		},
	}
}

// ---------------- helpers ----------------

func printServices(list []proto.ServiceDTO) {
	if len(list) == 0 {
		fmt.Println("（无）")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tPROTO\tLOCAL\tPUBLIC\tENABLED")
	for _, s := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%v\n", s.ID, s.Name, s.Protocol, s.LocalAddr, s.PublicPort, s.Enabled)
	}
	_ = w.Flush()
}

func printAllServices(list []proto.ServiceBriefDTO) {
	if len(list) == 0 {
		fmt.Println("（无）")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDEVICE\tNAME\tPROTO\tPUBLIC\tENABLED")
	for _, s := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%v\n", s.ID, s.DeviceName, s.Name, s.Protocol, s.PublicPort, s.Enabled)
	}
	_ = w.Flush()
}

func printForwards(list []proto.ForwardDTO) {
	if len(list) == 0 {
		fmt.Println("（无）")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLOCAL_PORT\tREMOTE_SERVICE\tREMOTE_DEVICE\tREMOTE_PORT\tPROTO\tENABLED")
	for _, f := range list {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%d\t%s\t%v\n",
			f.ID, f.LocalPort, f.RemoteServiceName, f.RemoteDeviceID, f.RemotePublicPort, f.Protocol, f.Enabled)
	}
	_ = w.Flush()
}
