package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/config"
	"github.com/zhiying8710/oh-my-lan/internal/enroll"
	"github.com/zhiying8710/oh-my-lan/internal/logging"
	"github.com/zhiying8710/oh-my-lan/internal/server"
	"github.com/zhiying8710/oh-my-lan/internal/store"
	"github.com/zhiying8710/oh-my-lan/internal/version"
)

// 通过短别名调用 auth 包，让 admin token 子命令读起来更聚焦。
var (
	authNewAdminToken = auth.NewAdminToken
	authNewRandomID   = auth.NewRandomID
	authHashSecret    = auth.HashSecret
)

func main() {
	if err := newRootCmd().ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "omlserver",
		Short: "oh-my-lan 服务端",
		Long:  "oh-my-lan 服务端：管理设备接入、服务发布、隧道转发。",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServer(cmd.Context(), configPath)
		},
		SilenceUsage: true,
	}

	cmd.PersistentFlags().StringVarP(&configPath, "config", "c", "configs/server.example.yaml", "服务端配置文件路径")
	cmd.AddCommand(
		newTokenCmd(&configPath),
		newDeviceCmd(&configPath),
		newAdminCmd(&configPath),
		newVersionCmd(),
	)
	return cmd
}

func newAdminCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "管理 Web Admin UI 的访问凭证（推荐：admin user，机器凭证：admin token）",
	}
	cmd.AddCommand(newAdminTokenCmd(configPath), newAdminUserCmd(configPath))
	return cmd
}

func newAdminUserCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "管理用于 Web/Tauri 账号密码登录的 admin 用户",
		Long: `推荐的登录方式：
  omlserver admin user set --username admin --password 'YourPassword'
然后在 Web UI / Tauri 输入用户名+密码即可登录，无需手动填 token。

如果 --password 留空，从 stdin 读一行：
  read -s PW && omlserver admin user set --username admin --password "$PW"`,
	}

	var username, password string
	setCmd := &cobra.Command{
		Use:   "set",
		Short: "新建或更新 admin 用户密码",
		RunE: func(cmd *cobra.Command, args []string) error {
			if username == "" {
				return fmt.Errorf("--username 必填")
			}
			if password == "" {
				p, err := readPasswordFromStdin()
				if err != nil {
					return err
				}
				password = p
			}
			if len(password) < 4 {
				return fmt.Errorf("密码至少 4 字符")
			}
			return runAdminUserSet(cmd.Context(), *configPath, username, password)
		},
	}
	setCmd.Flags().StringVar(&username, "username", "", "用户名")
	setCmd.Flags().StringVar(&password, "password", "", "密码；留空则从 stdin 读一行（不回显由调用方负责）")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "列出全部 admin 用户",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminUserList(cmd.Context(), *configPath)
		},
	}

	delCmd := &cobra.Command{
		Use:   "delete <username>",
		Short: "删除 admin 用户（同步删除其全部 session）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminUserDelete(cmd.Context(), *configPath, args[0])
		},
	}

	cmd.AddCommand(setCmd, listCmd, delCmd)
	return cmd
}

func readPasswordFromStdin() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err.Error() != "EOF" && line == "" {
		return "", fmt.Errorf("从 stdin 读密码失败: %w", err)
	}
	pw := strings.TrimRight(line, "\r\n")
	if pw == "" {
		return "", fmt.Errorf("密码为空")
	}
	return pw, nil
}

func runAdminUserSet(ctx context.Context, configPath, username, password string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	// 已存在则更新；不存在则插入
	if existing, err := st.GetAdminUserByUsername(ctx, username); err == nil {
		if err := st.UpdateAdminUserPassword(ctx, username, hash); err != nil {
			return err
		}
		fmt.Printf("已更新用户 %s（id=%s）的密码\n", existing.Username, existing.ID)
		fmt.Println("⚠️  已有会话仍可使用直到过期；如要立即踢出请用 `omlserver admin user delete` 再重建。")
		return nil
	}

	id, err := auth.NewRandomID()
	if err != nil {
		return err
	}
	if err := st.CreateAdminUser(ctx, store.AdminUser{
		ID: id, Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return err
	}
	fmt.Printf("已创建用户 %s（id=%s）\n", username, id)
	fmt.Println("现在可以在浏览器或 Tauri 客户端用此账号密码登录。")
	return nil
}

func runAdminUserList(ctx context.Context, configPath string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	list, err := st.ListAdminUsers(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("（无 admin 用户。请先运行 `omlserver admin user set` 创建）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUSERNAME\tCREATED\tUPDATED")
	for _, u := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			u.ID, u.Username,
			u.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			u.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func runAdminUserDelete(ctx context.Context, configPath, username string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.DeleteAdminUser(ctx, username); err != nil {
		return err
	}
	fmt.Printf("已删除用户 %s 及其全部 session\n", username)
	return nil
}

func newAdminTokenCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "管理 admin token",
	}

	var label string
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "生成一个新的 admin token",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminTokenCreate(cmd.Context(), *configPath, label)
		},
	}
	createCmd.Flags().StringVar(&label, "label", "", "可选备注，便于识别（不影响认证）")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "列出所有 admin token",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminTokenList(cmd.Context(), *configPath)
		},
	}

	revokeCmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "撤销指定 admin token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminTokenRevoke(cmd.Context(), *configPath, args[0])
		},
	}

	cmd.AddCommand(createCmd, listCmd, revokeCmd)
	return cmd
}

func runAdminTokenCreate(ctx context.Context, configPath, label string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	raw, err := authNewAdminToken()
	if err != nil {
		return err
	}
	id, err := authNewRandomID()
	if err != nil {
		return err
	}
	if err := st.CreateAdminToken(ctx, store.AdminToken{
		ID:        id,
		TokenHash: authHashSecret(raw),
		Label:     label,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("写入 admin_token: %w", err)
	}
	fmt.Printf("admin token: %s\n", raw)
	fmt.Printf("id:           %s\n", id)
	if label != "" {
		fmt.Printf("label:        %s\n", label)
	}
	fmt.Println("⚠️  请立刻保存上面的 token；它只会显示一次。")
	fmt.Println("用法：在浏览器打开 http://<server>:8080/admin/ ，输入此 token 登录。")
	return nil
}

func runAdminTokenList(ctx context.Context, configPath string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	list, err := st.ListAdminTokens(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("（无）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLABEL\tCREATED\tLAST_USED")
	for _, t := range list {
		last := "-"
		if t.LastUsedAt != nil {
			last = t.LastUsedAt.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			t.ID, t.Label, t.CreatedAt.Local().Format("2006-01-02 15:04:05"), last)
	}
	return w.Flush()
}

func runAdminTokenRevoke(ctx context.Context, configPath, id string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.DeleteAdminToken(ctx, id); err != nil {
		return err
	}
	fmt.Printf("已撤销 admin token %s\n", id)
	return nil
}

func newDeviceCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "本机直接查看 / 撤销已注册设备",
		Long:  "device 子命令直接操作 DB，不通过 HTTP API；服务端运行时调用是安全的。",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "列出全部已注册设备",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runDeviceList(cmd.Context(), *configPath)
			},
		},
		&cobra.Command{
			Use:   "revoke <id>",
			Short: "撤销设备：从 DB 删除（级联删除其服务）",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runDeviceRevoke(cmd.Context(), *configPath, args[0])
			},
		},
	)
	return cmd
}

func runDeviceList(ctx context.Context, configPath string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	list, err := st.ListDevices(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("（无）")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS\tLAST_SEEN\tCREATED")
	for _, d := range list {
		last := "-"
		if d.LastSeenAt != nil {
			last = d.LastSeenAt.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			d.ID, d.Name, d.Status, last, d.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}

func runDeviceRevoke(ctx context.Context, configPath, deviceID string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	dev, err := st.GetDeviceByID(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("查询 device %s: %w", deviceID, err)
	}
	if err := st.DeleteDevice(ctx, deviceID); err != nil {
		return err
	}
	fmt.Printf("已撤销设备：%s（name=%s）\n", deviceID, dev.Name)
	fmt.Println("⚠️  若 omlserver 正在运行，请同时重启它以清空 chisel UserIndex 中的该用户")
	return nil
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.String())
		},
	}
}

func newTokenCmd(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "生成 / 管理 enrollment token（本机直接操作 DB，无需 HTTP）",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "create",
		Short: "生成一个一次性 enrollment token，立刻打印明文",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTokenCreate(cmd.Context(), *configPath)
		},
	})
	return cmd
}

func runTokenCreate(ctx context.Context, configPath string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("创建 data_dir: %w", err)
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	issued, err := enroll.New(st).IssueToken(ctx, 0)
	if err != nil {
		return fmt.Errorf("生成 token: %w", err)
	}
	fmt.Printf("token: %s\n过期时间: %s\n", issued.Token, issued.ExpiresAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Println("⚠️  请立刻把上面这个 token 用于 `omlctl enroll`；它只会显示一次。")
	return nil
}

func runServer(ctx context.Context, configPath string) error {
	cfg, err := config.LoadServer(configPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	// 同时写 stderr + ring buffer：buffer 提供 /api/admin/logs 给 UI「服务端」tab 实时拉
	logBuf := logging.NewRingBuffer(1000)
	logger := logging.NewWithBuffer(os.Stderr, logging.Options{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
	}, logBuf)

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("创建 data_dir: %w", err)
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "oml.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	srv, err := server.New(server.Options{
		ListenAddr:          cfg.ListenAddr,
		ChiselListenAddr:    cfg.ChiselListenAddr,
		ChiselAdvertiseAddr: cfg.ChiselAdvertiseAddr,
		ChiselKeySeed:       cfg.ChiselKeySeed,
		ChiselVerbose:       cfg.Log.Level == "debug",
		DataDir:             cfg.DataDir,
		PortMin:             cfg.PortPool.Min,
		PortMax:             cfg.PortPool.Max,
		Store:               st,
		Logger:              logger,
		LogBuffer:           logBuf,
	})
	if err != nil {
		return err
	}

	logger.Info("omlserver 启动",
		"version", version.Version,
		"listen", cfg.ListenAddr,
		"chisel", cfg.ChiselListenAddr,
		"port_pool", fmt.Sprintf("%d-%d", cfg.PortPool.Min, cfg.PortPool.Max),
	)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Start(sigCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("omlserver 已停止")
	return nil
}
