BINDIR := bin
SERVER := $(BINDIR)/omlserver
CLIENT := $(BINDIR)/omlctl

MODULE := github.com/zhiying8710/oh-my-lan

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "\
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.BuildTime=$(BUILD_TIME)"

.PHONY: build server ctl test vet tidy clean run-server run-ctl tauri-sync tauri-dev tauri-build tauri-bin tauri-sidecar tauri-pack icons install-hooks

build: server ctl

# install-hooks: 把 .githooks/ 设为 git hooks 目录。一次性操作。
# 加 hook 后每次 commit 自动跑 web-lint / go vet / go test / cargo test（按需）。
install-hooks:
	git config core.hooksPath .githooks
	@echo "[hooks] ✓ pre-commit hook 启用；跳过用 git commit --no-verify"

server:
	@mkdir -p $(BINDIR)
	go build $(LDFLAGS) -o $(SERVER) ./cmd/omlserver

ctl:
	@mkdir -p $(BINDIR)
	go build $(LDFLAGS) -o $(CLIENT) ./cmd/omlctl

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(BINDIR)

run-server: server
	$(SERVER) --config configs/server.example.yaml

run-ctl: ctl
	$(CLIENT) --help

# --- Tauri 桌面壳 ---
#
# tauri-sync: 把 web/ 静态资源拷到 tauri/dist/（同一份代码自适应 Tauri 环境）
# tauri-dev:  cargo run（开发，自动同步 dist）
# tauri-build: cargo tauri build（产 .app/.exe/.AppImage；需要 cargo install tauri-cli）

# tauri/dist 是从 internal/server/web/ 镜像出来的副本，**永远 always-regenerate**：
# 每次 make tauri-* 目标都先 cp 覆盖一遍，保证 Tauri webview 跑的是最新前端。
# 因为是镜像，所以入 .gitignore 不追踪；唯一来源是 internal/server/web/。
tauri-sync:
	@mkdir -p tauri/dist
	@rm -rf tauri/dist/app
	cp -f internal/server/web/index.html internal/server/web/style.css internal/server/web/app.js \
	      internal/server/web/favicon.svg internal/server/web/favicon-32.png tauri/dist/
	@cp -R internal/server/web/app tauri/dist/app

# web-lint：极简前端健全性检查（带历史教训）。
# ① 历史教训 1：Edit 静默漏改 → app.js 引用已删除的 DOM id → 浏览器空白 + TypeError
# ② 历史教训 2：在 Tauri 2.x WKWebView 中声明 `const isTauri` 跟注入的全局只读
#    属性 window.isTauri 冲突 → SyntaxError → 整个 IIFE 不执行 → 窗口空白
# ③ 历史教训 3：危险操作走浏览器原生 confirm()——Tauri WKWebView 下样式僵硬不可控、
#    影响面文案被压成一行；DESIGN.md Native Dialog Rule + 第 4 条 Design Principle
#    要求所有 confirm/alert 走自家 <dialog> + showAlert/showConfirm。
#
# 因此 lint 同时检查：
#   - 所有 getElementById('xxx') 的 xxx 在 index.html 里有对应 id="xxx"
#   - app.js 不能用某些跟 webview 注入的全局冲突的变量名（黑名单）
#   - app.js 不能直接调用 window 级的 confirm()/alert()/prompt()
JS_GLOBAL_BLACKLIST = isTauri

web-lint:
	@set -e; \
	HTML=internal/server/web/index.html; \
	JS_FILES=$$(find internal/server/web -maxdepth 2 -name '*.js' -type f); \
	for ref in $$(grep -ohE "getElementById\('[a-z-]+'\)" $$JS_FILES | sed -E "s/.*'([^']+)'.*/\1/" | sort -u); do \
		grep -q "id=\"$$ref\"" $$HTML || { echo "[web-lint] JS 引用了不存在的 id: $$ref"; exit 1; }; \
	done; \
	for name in $(JS_GLOBAL_BLACKLIST); do \
		if grep -hE "^\s*(const|let|var)\s+$$name\b" $$JS_FILES >/dev/null; then \
			echo "[web-lint] JS 用 const/let/var 声明了 \"$$name\"，会跟 Tauri 注入的全局冲突 (SyntaxError)"; \
			exit 1; \
		fi; \
	done; \
	hits=$$(grep -nE "(^|[^a-zA-Z_.])(confirm|alert|prompt)\\s*\\(" $$JS_FILES \
	         | grep -vE "//.*(confirm|alert|prompt)" \
	         | grep -vE "(showConfirm|showAlert|resolveAlert|alertModal|alertMessage|alertTitle|alertOkBtn|alertCancelBtn|enrollMsg|autostartMsg|daemonMsg|enrollGenerateTokenBtn|enrollTokenInput|enrollNameInput|enrollForm|enrollCard|enrollServerDisplay|placeholder=\"ot)" || true); \
	if [ -n "$$hits" ]; then \
		echo "[web-lint] JS 直接调用了 window.confirm/alert/prompt——必须用 showConfirm/showAlert："; \
		echo "$$hits"; \
		exit 1; \
	fi; \
	echo "[web-lint] ✓ DOM id / 全局名冲突 / 原生 confirm-alert 检查通过 ($$(echo $$JS_FILES | wc -w | tr -d ' ') 个 JS 文件)"

test: web-lint

tauri-dev: tauri-sync
	cd tauri/src-tauri && cargo run

tauri-build: tauri-sync
	@command -v cargo-tauri >/dev/null 2>&1 || { echo "请先: cargo install tauri-cli --version '^2'"; exit 1; }
	cd tauri/src-tauri && cargo tauri build

# tauri-bin: 跑得快的"开发部署"——增量 cargo build --release，不打包成 .app。
# 适合演示时让 binary 跟最新 web 资源对齐（无需 cargo tauri build 的打包流程）。
tauri-bin: tauri-sync
	cd tauri/src-tauri && cargo build --release
	@echo "binary: tauri/src-tauri/target/release/oh-my-lan-desktop"

# tauri-sidecar: 在 tauri/src-tauri/binaries/ 下产出对应当前 host triple 的 omlctl，
# 供 Tauri 把它作为 .app 内置二进制打包。Windows / Linux / 跨架构由 CI 处理。
tauri-sidecar:
	@mkdir -p tauri/src-tauri/binaries
	@triple=$$(rustc -vV | sed -n 's|host: ||p'); \
	 case $$triple in \
	   *darwin*) GOOS=darwin; ext=;; \
	   *linux*) GOOS=linux; ext=;; \
	   *windows*) GOOS=windows; ext=.exe;; \
	 esac; \
	 case $$triple in \
	   aarch64-*) GOARCH=arm64;; \
	   x86_64-*) GOARCH=amd64;; \
	 esac; \
	 echo "==> sidecar omlctl-$$triple$$ext (GOOS=$$GOOS GOARCH=$$GOARCH)"; \
	 GOOS=$$GOOS GOARCH=$$GOARCH CGO_ENABLED=0 \
	   go build -o tauri/src-tauri/binaries/omlctl-$$triple$$ext ./cmd/omlctl

# 完整 DMG / installer 流水线。
# macOS 走 pack-macos.sh 三步（build .app → 注入 ATS → bundle DMG），
# 解决 Tauri 自身在 .app 和 .dmg 之间没有 post-build hook 的痛点。
tauri-pack: tauri-sync tauri-sidecar
	@command -v cargo-tauri >/dev/null 2>&1 || { echo "请先: cargo install tauri-cli --version '^2'"; exit 1; }
	@case $$(uname -s) in \
	  Darwin) ./tauri/scripts/pack-macos.sh ;; \
	  *) cd tauri/src-tauri && cargo tauri build ;; \
	esac
	@echo "[tauri-pack] ✓ 产物见 tauri/src-tauri/target/release/bundle/"

# icons: 从 icons/icon.svg 重新渲染全套尺寸 + .icns + .ico
# 改图标时只动 SVG，跑 make icons 自动更新所有派生文件。
#
# 渲染器用 librsvg 的 rsvg-convert：ImageMagick 内置 MSVG 不解析 linearGradient
# 也不画 stroke="…" fill="none" 的线条，曾踩过坑（背景纯黑、连接线丢失）。
# librsvg 是 Firefox/GNOME 同款，渲染保真。
ICON_DIR := tauri/src-tauri/icons
icons:
	@command -v rsvg-convert >/dev/null 2>&1 || { echo "需要 librsvg: brew install librsvg"; exit 1; }
	@command -v magick >/dev/null 2>&1 || { echo "需要 ImageMagick (打 .ico): brew install imagemagick"; exit 1; }
	@command -v iconutil >/dev/null 2>&1 || { echo "需要 iconutil (macOS 自带)"; exit 1; }
	@cd $(ICON_DIR); \
	for size in 16 32 64 128 256 512 1024; do \
		rsvg-convert -w $$size -h $$size icon.svg -o $${size}x$${size}.png; \
	done; \
	cp 256x256.png 128x128@2x.png; \
	cp 512x512.png icon.png; \
	cp 256x256.png Square150x150Logo.png; \
	cp 64x64.png   Square44x44Logo.png; \
	rm -rf icon.iconset && mkdir icon.iconset; \
	cp 16x16.png     icon.iconset/icon_16x16.png; \
	cp 32x32.png     icon.iconset/icon_16x16@2x.png; \
	cp 32x32.png     icon.iconset/icon_32x32.png; \
	cp 64x64.png     icon.iconset/icon_32x32@2x.png; \
	cp 128x128.png   icon.iconset/icon_128x128.png; \
	cp 256x256.png   icon.iconset/icon_128x128@2x.png; \
	cp 256x256.png   icon.iconset/icon_256x256.png; \
	cp 512x512.png   icon.iconset/icon_256x256@2x.png; \
	cp 512x512.png   icon.iconset/icon_512x512.png; \
	cp 1024x1024.png icon.iconset/icon_512x512@2x.png; \
	iconutil -c icns icon.iconset -o icon.icns; \
	rm -rf icon.iconset; \
	magick 16x16.png 32x32.png 64x64.png 128x128.png 256x256.png icon.ico; \
	cp icon.svg ../../../internal/server/web/favicon.svg; \
	cp 32x32.png ../../../internal/server/web/favicon-32.png
	@echo "[icons] ✓ 已从 $(ICON_DIR)/icon.svg 重新生成全套图标，并同步 favicon"
