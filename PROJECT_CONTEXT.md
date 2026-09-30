# Desktop Guard Pro 项目上下文

Desktop Guard Pro 是 Windows x64 本机保护与审计应用。Go 服务负责采集、
会话、存储和分析；交互式代理负责用户桌面输入；WinUI 3 提供七页原生界面；
维护程序与 WiX MSI 负责安装生命周期。

## 目录职责

源码、交付文档和已发布文件分别保存。

| 路径 | 职责 |
| --- | --- |
| `backend/go.mod`、`backend/go.sum` | Go 模块与依赖锁定。 |
| `backend/cmd/` | 服务、代理、维护程序和发布工具入口。 |
| `backend/internal/` | 领域模型、采集、存储、风险、报告和 Windows 集成。 |
| `frontend/DesktopGuardPro.Native/` | WinUI 3 原生桌面应用。 |
| `frontend/DesktopGuardPro.Native.Tests/` | 跨页状态、报告范围、模式草稿和输入策略回归。 |
| `docs/context/` | 前端与后端定位说明。 |
| `docs/technical-architecture.md` | 架构、按钮、接口与操作流程。 |
| `docs/requirements-acceptance-matrix.md` | 功能要求及验收边界。 |
| `docs/verification/current.md` | 最新工作区验证记录。 |
| `docs/releases/` | 当前发布说明。 |
| `releases/2.11.41/` | GitHub 当前发布版本及本地原始发布物，Git 忽略。 |
| `releases/2.11.40/` | 历史发布版本及本地原始发布物。 |
| `releases/2.11.39/` | 保留的历史原始发布物。 |
| `releases/2.11.38/` | 保留原始发布物，不能混入新构建或重新签名。 |
| `assets/` | 图标及安装器图片。 |
| `installer/` | WiX 产品定义。 |
| `scripts/` | 构建、签名、图标生成及发布检查。 |
| `dist/` | 可重新生成的临时构建目录，Git 忽略。 |

## 运行进程与数据

四个程序通过本机命名管道协调，实际运行数据独立于源码目录。

- `desktop-guard-service.exe`：Windows Service、授权、采集和持久化。
- `desktop-guard-agent.exe`：用户会话活动、输入设备、钩子与解锁窗口。
- `desktop-guard-ui.exe`：C# 原生界面与分析操作。
- `desktop-guard-maintenance.exe`：安装、升级、卸载、健康检查和事务恢复。

默认数据位于 `%ProgramData%\DesktopGuardPro`，包含 SQLite、DPAPI 密钥、
安装策略和维护记录。目录清理不涉及这些运行数据。
事件负载经 AES-GCM 加密，HMAC 链和检查点用于核对完整性。

## 技术栈与构建

完整构建在 Windows x64 上执行，首次构建需要获取 Go 和 NuGet 依赖。

- Go：模块声明 `1.26.0`，指定工具链 `1.26.6`。
- .NET 8、Windows App SDK `1.8.260508005`。
- Windows SDK BuildTools `10.0.26100.7705`。
- WiX Toolset 3.14，以及 Windows SDK 的签名和清单工具。
- `modernc.org/sqlite`、`go-winio`、Walk、win 和 `x/sys/windows`。

从项目根目录构建未签名的开发组件：

```powershell
go -C backend run ./cmd/desktop-guard-release `
  --version 2.11.41 --output ../dist/staging
```

单独构建原生界面：

```powershell
dotnet build frontend/DesktopGuardPro.Native/DesktopGuardPro.Native.csproj `
  -c Release -p:Platform=x64
```

MSI 发布使用 `scripts/build-msi.ps1`，需要有效签名证书。默认输出到
`releases/<版本>/`，已有安装包会阻止覆盖。完整命令见 [README](README.md)。

## 自动化检查

从根目录执行下列命令，Go 使用 `-C backend` 指定模块位置。

```powershell
go -C backend mod verify
go -C backend test ./... -count=1 -timeout=120s
go -C backend vet ./...
dotnet run --project frontend/DesktopGuardPro.Native.Tests -c Release
.\scripts\test-native-ui-shell.ps1
.\scripts\test-msi-package.ps1
.\scripts\test-msi-release.ps1
```

指针和并发检查使用专门入口：

```powershell
go -C backend test ./internal/deskguard/hook -count=1 -gcflags=all=-d=checkptr=2
go -C backend test -race ./internal/deskguard/hook ./internal/agent `
  ./internal/service -count=1 -timeout=120s
```

## 配置与行为约束

这些约束影响界面、采集和报告之间的关系。

- 进行中的保护会话使用创建时策略快照，模式保存供以后创建的会话使用。
- 活动、暂停、降级和收尾中的会话不允许更换目标或排除范围。
- 历史、审计、风险、资产和报告通过所选分析会话关联，异步响应需要验证修订号。
- 临时键鼠锁定与保护会话分别运行；保护结束使用 Windows 凭据，临时解锁使用
  本地密码或恢复码。
- 采集缺口、未完成基线和完整性结果必须按实际数据展示。
- 发布包与工作区源码分别说明，未签名构建不能覆盖已发布安装包。

模块定位见 [后端说明](docs/context/backend.md) 和
[前端说明](docs/context/frontend.md)。
