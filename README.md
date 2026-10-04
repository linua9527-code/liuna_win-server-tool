# WinServerTool

Windows 基础配置与本机痕迹清理工具。发布产物为单个 `win-server-tool.exe`，不需要安装 Go、.NET 或第三方 DLL。

## 构建

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags="-s -w" -o dist/win-server-tool.exe .
```

支持 Windows 10、Windows 11 和 Windows Server 2016 及以上版本，配置和清理操作要求以管理员身份运行。Windows 10/11 家庭版等缺少远程桌面主机功能的版本需关闭远程桌面配置，其他功能仍可使用。

## 使用

不带参数启动即可进入中文交互菜单，也可以直接双击 EXE。菜单支持编辑远程桌面、登录锁定、时区、语言、密码、清理范围和执行时间，并提供一键配置清理、单独配置、单独清理、计划任务管理、状态查看和配置预检。

```powershell
.\win-server-tool.exe
.\win-server-tool.exe interactive --config config.json
.\win-server-tool.exe --config config.json
```

交互模式读取所选配置文件；文件不存在时使用默认配置。菜单中回车保留当前值，`-` 清空可选文本。选择“一键配置并清理”“系统配置”或“清理记录”后直接执行，安装定时任务也直接执行，不再要求额外确认。编辑仅更新本次会话，选择“保存配置”后写入文件；退出不会自动保存。运行操作直接使用本次会话的选项，不要求预先保存。密码在执行时隐藏输入，不写入配置。输入结束时退出，操作失败时显示错误并返回菜单。

带命令和参数的启动方式继续用于脚本和自动化。参数同时支持 `-config` 和 `--config`：

```powershell
win-server-tool.exe init
# 编辑 config.json
win-server-tool.exe status
win-server-tool.exe preflight --config config.json
win-server-tool.exe apply --config config.json
win-server-tool.exe clean --config config.json
win-server-tool.exe schedule install --config config.json
win-server-tool.exe schedule remove
```

`init` 创建可编辑的配置文件，`config.example.json` 提供完整字段示例。`apply` 只执行系统配置；`setup` 在系统配置成功后继续清理，一条命令执行两阶段操作：

```powershell
win-server-tool.exe setup --config config.json
```

默认时区为 `China Standard Time`（上海所在的 UTC+8），语言为 `zh-CN`，RDP 端口为 `3389`，登录失败阈值为 5 次，锁定及计数重置周期均为 15 分钟。`rdp.enabled=false` 表示跳过远程桌面配置；账户锁定由 `configure_lockout` 单独控制。密码修改默认关闭，启用 `password.change` 后通过控制台隐藏输入；空用户名表示当前本地账户。密码至少 12 个字符且必须满足服务器自身密码策略，配置文件拒绝保存密码字段。

远程端口修改保留当前会话，新端口需重启服务器后生效。请提前放行云平台安全组中的新 TCP/UDP 端口。失败时会回滚本次 RDP 注册表及新增防火墙规则；语言、密码、时区和账户策略属于独立步骤，不进行全局回滚，失败时应根据输出检查已经生效的步骤。程序保留系统已有防火墙规则，`remote_addresses` 仅限制本工具创建的规则，其他放行规则可能仍允许连接。

语言包缺失时需启用 `language.install_if_missing`，并在 `language.package_cab` 指定与系统版本、体系结构匹配的本机 CAB 文件；具备 `Install-Language` 接口的系统也可使用在线安装。安装可能需要网络、Windows Update 和重启。Server Core 或单语言版本是否支持界面语言取决于系统镜像。用户界面语言需注销后生效，系统区域语言需重启。`copy_to_system` 需要系统提供 `Copy-UserInternationalSettingsToSystem` 接口，默认关闭；启用后也将设置复制到欢迎屏幕及新建用户。

本地账户策略可能被域组策略覆盖，内置 Administrator 的锁定行为由 Windows 版本及系统策略决定。域控制器不执行本工具的本地账户锁定和本地密码修改；域账户应使用域管理工具。

全部已有清理项默认开启：当前用户的 RDP MRU、服务器记录、标准位置的 `Default.rdp`、远程桌面跳转列表、保存的远程凭据（`TERMSRV/*`）、最近访问与运行历史、PowerShell PSReadLine 历史、用户及 Windows 临时文件、全部 Windows 事件日志。临时文件不按时间筛选，尝试删除全部文件和空子目录。占用、权限受限和重解析项会报告跳过情况；清理不遍历其他用户，不删除业务文件。现有配置中的 `cleanup.temp_min_age_days` 字段应删除，显式关闭的清理项仍按配置处理。

无需额外确认参数，直接运行清理：

```powershell
win-server-tool.exe clean
```

默认配置文件缺失时直接使用内置默认值；显式指定 `--config` 时该文件应存在。事件日志逐通道尝试清空，包括安全日志和远程桌面连接、登录、注销、会话断开记录；某个通道失败仍继续处理其他通道和清理项目，最后汇总错误并返回非零退出码。程序不导出或备份待清理日志。Windows 后续运行仍会产生新日志，包括清空安全日志自身的审计事件；本机清理不等于匿名访问，云平台、网关和远端记录不在本机清理范围内。

## 定时清理

```powershell
win-server-tool.exe schedule install --config config.json
win-server-tool.exe schedule remove
```

程序和配置会复制到 `%ProgramData%\WinServerTool`，目录访问限于管理员和 SYSTEM。主任务 `WinServerTool-Cleanup` 以 SYSTEM 身份每天执行，只处理安装任务时记录的用户；用户注销时也可运行。执行时间按服务器当前时区计算。任务运行中禁止更新或删除。

远程凭据归属用户凭据库，独立任务 `WinServerTool-RDPCredentials` 使用安装用户的交互令牌，在该用户登录期间每日执行，并在登录时补充清理；用户注销期间不访问该用户凭据库。程序不保存账户密码。启用远程凭据清理会影响该用户已保存的 RDP 登录信息。

修改原配置文件后需重新安装任务，任务不会自动读取原文件的后续修改。删除任务保留受限目录中的程序和配置副本，不影响其他计划任务。可以在 Windows 任务计划程序中查看执行状态和退出码。

## GitHub Actions

推送任意分支或提交 PR 会执行格式检查、`go vet`、配置校验与只读 Windows 集成测试，再构建 Windows x64 EXE 和 SHA256 校验文件，结果可从 Actions 的 Artifacts 下载。推送 `v*` 标签会额外创建 GitHub Release 并上传 EXE 与校验文件；仅发布任务申请 `contents: write` 权限。

```powershell
git tag v0.1.0
git push origin v0.1.0
```

本项目使用 Windows 自带 PowerShell 5.1、系统管理模块与 Win32 API，脚本内嵌在 EXE 中，无第三方依赖。目标机器需允许这些 Windows 管理组件执行。CI 不修改 RDP、账户密码、语言或系统日志；这些操作应在可恢复的 Windows Server 实例上验收。
