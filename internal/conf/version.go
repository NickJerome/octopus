package conf

var (
	Version          = "dev"
	Commit           = "unknown"
	BuildTime        = "unknown"
	Author           = "bestrui"
	Repo             = "https://github.com/NickJerome/octopus"
	UpdateRepository = "NickJerome/octopus" // 发布构建可通过 -ldflags 注入所属仓库。
)
