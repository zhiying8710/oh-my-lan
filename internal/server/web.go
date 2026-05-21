package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webFS embed.FS

// adminWebHandler 返回 /admin/ 路径的静态文件 handler。
// 资源用 embed.FS 打包进二进制；无需外部文件依赖。
func adminWebHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// 编译期 embed 已保证目录存在；运行期到这里说明仓库结构损坏。
		panic("web/ 目录嵌入失败: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.StripPrefix("/admin/", fileServer)
}
