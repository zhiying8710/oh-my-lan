package version

import "fmt"

// 通过 -ldflags 在构建时注入。默认值用于 `go run` 等未走 Makefile 的场景。
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func String() string {
	return fmt.Sprintf("oh-my-lan %s (commit=%s, built=%s)", Version, Commit, BuildTime)
}
