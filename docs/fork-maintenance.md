# 自有版本维护与发布

本仓库的更新源是 `NickJerome/octopus`。上游 `bestruirui/octopus` 用于合并源码；自有功能与上游修改经过验证后，统一从本仓库发布。Go module 路径、原作者信息和许可证保留，避免无必要的导入路径改动。

## 日常维护

- 在 `master` 保留可发布的自有版本，功能修改使用独立分支。
- 渠道请求转换和更新机制属于两个变更主题，建议分开提交，方便排查和回退。
- 每次合并上游后，重点检查渠道数据字段、三种协议转换、参数覆盖执行顺序、更新源和发布工作流。
- 请求转换说明见 [使用指南](channel-request-transform.md) 与 [设计说明](channel-request-transform-design.md)。当前移除预设按约定使用 `<total_tokens>[\s\S]*`，会删除该标记到当前文本块结尾的全部内容。

首次添加上游远程（已存在时跳过）：

```bash
git remote add upstream https://github.com/bestruirui/octopus.git
```

在工作区已提交干净的前提下，同步上游：

```bash
git fetch upstream --no-tags
git switch master
git switch -c maintenance/merge-upstream
git merge upstream/master
```

解决冲突、运行检查，审查更新源仍为自有仓库，然后再合回 `master`。不要直接用上游发布包覆盖正在运行的自有版本。合并时尤其要保留 `.github/workflows/release.yaml` 的标签触发方式。

## 发布规则

正式版本只接受 `v主版本.次版本.修订号`，例如 `v0.14.0`、`v0.14.1`。数字不带前导零，版本号必须递增；`dev`、`-beta`、`-custom` 等标记不用于在线升级。维护自己的版本序列，不必与上游版本号保持一致。

`main.go` 中历史版本注释不再触发发布。普通分支推送与 PR 只做 CI 检查；**推送正式版本标签会公开 GitHub Release 并推送 GHCR 镜像**。

先确认 GitHub 仓库启用 Actions，工作流拥有仓库内容和 Packages 写入权限。镜像推送使用内置 `GITHUB_TOKEN`，不需要 Docker Hub 密钥。首次创建的 GHCR 包可能是私有的：公开部署时将包可见性设为 public，私有部署则先登录 GHCR。

本地基础检查：

```bash
go test ./...
go vet ./...
cd web
pnpm install --frozen-lockfile
pnpm run build
cd ..
git restore -- static/out/README.md
```

测试渠道时，覆盖 OpenAI Chat、Responses、Anthropic 三种协议的预览与实际请求；确认思考强度、删除标记、失败重试不会相互污染。仓库贡献规范要求 PR 不包含测试文件；临时验证用例在提交前清理。

所有改动审查并提交后，在正确的发布提交上执行以下命令（版本号是示例，使用前确认尚未使用且高于最近自有发布版本）：

```bash
git switch master
git status --short
git push origin master
git tag -a v0.14.0 -m "Custom release v0.14.0"
git push origin v0.14.0
```

发布流水线会：

1. 校验标签格式，确认高于本仓库最近正式发布版本。
2. 将同一个版本号写入前端和后端，将 `github.repository` 写入后端更新源。
3. 构建 Windows、Linux、macOS、Android ZIP，生成 `SHA256SUMS`，上传到草稿 Release。
4. 推送 `ghcr.io/nickjerome/octopus:v0.14.0` 和 `:latest` 多平台镜像。
5. 公开完整 Release，将其标为 Latest，供在线升级使用。

构建失败时修复原因再重新运行失败任务。未公开的草稿不会被升级接口使用。已公开版本不要覆盖文件或移动标签，修复应发布更大的新版本号。旧版本的 `SHA256SUMS` 必须与该版本的 ZIP 一起保留。

## 第一次迁移到自有版本

原官方程序仍内置官方更新源，**不能靠原程序的“立即更新”切换到本仓库**。先发布本仓库版本，再手动安装一次：

1. 停止现有服务，备份 `data` 目录；外部 MySQL/PostgreSQL 另做数据库备份。
2. 下载本仓库对应系统与架构的 ZIP 和 `SHA256SUMS`，校验 ZIP 的 SHA256。
3. 保存旧二进制，替换为 ZIP 内的 `octopus` 或 `octopus.exe`。保留启动参数、配置文件和工作目录。
4. 重新启动，在“设置 → 版本信息”确认当前版本及更新源 `NickJerome/octopus`，验证关键渠道。

此后的裸机二进制部署可通过页面升级到本仓库更高的正式版本。开发运行的 `go run` / 普通 `go build` 显示 `dev`，不会在线替换自身。

## 更新配置

将以下配置合并到现有 `data/config.json`：

```json
{
  "update": {
    "repository": "NickJerome/octopus",
    "enabled": true
  }
}
```

可用环境变量覆盖：

| 变量 | 用途 |
| --- | --- |
| `OCTOPUS_UPDATE_REPOSITORY` | 更新源，格式 `owner/repo` |
| `OCTOPUS_UPDATE_ENABLED` | `true` 或 `false`，控制检查与安装 |
| `OCTOPUS_GITHUB_PAT` | 可选，访问私有仓库或提高 GitHub API 配额；令牌需要目标仓库 Contents 读取权限 |

配置与环境变量修改后重启生效。更新源无效、开发版本或显式禁用时不会下载发布包；源无法访问时不会回退到官方仓库。GitHub 的 404 也可能表示缺少私有仓库权限，不能仅凭它判定仓库没有发布版本。

页面显示的更新源来自后端配置。前端与后端都使用正式版本号时，版本不一致会提示刷新缓存。

## 在线升级的行为和边界

安装请求携带用户所选版本号。后端按该标签读取 Release，以该 Release 的资产 ID 下载对应平台 ZIP 与 `SHA256SUMS`，校验通过后才修改现有程序。只接受更高的正式版本；拒绝并发安装、草稿、预发布、缺失资产和校验失败。

新文件先写入当前程序目录并同步到磁盘，再将旧程序改名为 `<程序路径>.old`，最后安装新文件。安装替换失败时尝试恢复旧文件；成功后保留 `.old` 供人工恢复。进程需要对程序目录拥有写入权限。程序即使被重命名，也会按发布包里的标准文件名找到新版。

后端先返回并发送成功响应，再执行原有关闭流程并以原参数重启。页面最长等待约两分钟，检测到目标版本响应后清理本应用缓存并刷新。重启会断开正在处理的连接，请在空闲时升级；超时请检查日志和进程状态，不要连续点击更新。

**这不是完整的自动健康回滚。** `.old` 仅备份二进制，不包含配置或数据库。若新版本已启动但运行异常，需停止服务、按需恢复数据库备份，再手动恢复旧二进制。不要假设旧程序能读取新版本已迁移的数据库。Windows 服务管理器托管的实例建议关闭在线升级，通过服务管理器停止、替换和启动。

## Docker 部署

镜像默认设置 `OCTOPUS_UPDATE_ENABLED=false`，使用镜像替换升级：

```bash
docker compose pull
docker compose up -d
```

数据目录仍需持久挂载。生产环境可将 compose 的 `:latest` 改为明确版本（例如 `:v0.14.0`），以后每次手动调整标签再拉取。容器内在线替换二进制不会修改镜像，容器重建会丢失该替换，因此维持默认禁用。

## Windows 本地开发构建

以下 PowerShell 命令从仓库根目录运行，生成可供手动验证的开发版，不开放在线升级：

```powershell
$buildVersion = 'dev-local'
$env:VITE_APP_VERSION = $buildVersion
Push-Location web
pnpm install --frozen-lockfile
pnpm run build
Pop-Location
git restore -- static/out/README.md
go build -trimpath -tags=jsoniter -ldflags "-X github.com/bestruirui/octopus/internal/conf.Version=$buildVersion -X github.com/bestruirui/octopus/internal/conf.UpdateRepository=NickJerome/octopus" -o build/octopus.exe .
Remove-Item Env:VITE_APP_VERSION
```

完整多平台发布使用 GitHub Actions；`scripts/build.sh` 依赖 Ubuntu、Android NDK 与归档工具。手动使用此脚本时，需先构建前端并让 `VITE_APP_VERSION` 与 `BUILD_VERSION` 完全一致。未显式设置 `BUILD_VERSION` 时，脚本只有在干净工作区的精确正式标签上才选择正式版本，其余情况使用 `dev-<commit>`；`UPDATE_REPOSITORY` 控制编译进后端的默认更新源。
