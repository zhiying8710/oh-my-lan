package server

import (
	"embed"
	"io/fs"
	"net/http"
)

// 使用 `web`（不带 `/*`）以递归嵌入子目录——app.js 已拆成 `web/app/*.js` ES modules。
// 早期写法 `web/*` 只会匹配顶层文件，子目录加载会 404。
//
//go:embed web
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
