# 后端模块说明

`backend/` 是 Go 模块根目录。服务、代理、维护程序和发布工具共用
`desktopguardpro` 模块名，源码迁移没有改变包导入路径。

## 程序入口

按运行职责选择入口，再读取相关 `internal/` 模块。

| 入口 | 用途 |
| --- | --- |
| [服务](../../backend/cmd/desktop-guard-service/main.go) | Windows 服务启动；`runtime.go` 协调采集、恢复和收尾。 |
| [交互式代理](../../backend/cmd/desktop-guard-agent/main.go) | 用户会话代理、输入活动、键鼠控制及解锁。 |
| [维护程序](../../backend/cmd/desktop-guard-maintenance/main.go) | 安装升级、卸载、健康检查和 MSI 事务。 |
| [发布工具](../../backend/cmd/desktop-guard-release/main.go) | 三个 Go 程序、原生 UI、图标、许可文件和发布清单。 |

## 业务模块

这些模块之间通过领域对象、协议消息和仓库接口协作。

| 模块 | 职责 |
| --- | --- |
| [domain](../../backend/internal/domain/) | 会话状态、监控策略、事件和资产模型。 |
| [service](../../backend/internal/service/) | 状态机、授权、模式保存、分析和导出 API。 |
| [collector](../../backend/internal/collector/) | 文件、系统、资产及审计采集和会话生命周期。 |
| [storage](../../backend/internal/storage/) | SQLite、载荷加密、完整性链、查询和保留清理。 |
| [risk](../../backend/internal/risk/) | 风险规则、评分和证据关联。 |
| [reporting](../../backend/internal/reporting/) | 报告渲染、范围限制和敏感字段裁剪。 |
| [agent](../../backend/internal/agent/) | 用户会话、活动汇总、设备跟踪和控制协调。 |
| [deskguard](../../backend/internal/deskguard/) | 低级键鼠钩子、密码窗口和输入法处理。 |
| [contracts](../../backend/internal/contracts/)、[windows](../../backend/internal/windows/) | 消息契约、命名管道、身份及 Windows 系统接口。 |
| [maintenance](../../backend/internal/maintenance/) | 发布验证、备份、事务和回滚。 |
| [appbridge](../../backend/internal/appbridge/)、[desktop](../../backend/internal/desktop/) | 维护程序仍使用的健康探测、会话请求和环境检查。 |

`maintenance/runtime_files_windows.go` 提供发布和安装共用的完整运行文件清单。
四个 EXE 继续核对签名与摘要，WinUI 主程序集、依赖、配置和嵌套资源使用
`runtimeFiles` 保存相对路径、大小及 SHA-256。旧安装清单缺少该字段时仍可加载；
独立升级只移除旧清单明确登记的资源，未登记文件保留。

`maintenance/startup_windows.go` 继续管理机器级代理启动项，同时提供界面启动项
的卸载清理。维护程序按已验证安装所有者 SID 打开 `HKEY_USERS`，仅删除指向当前
安装 UI 路径的 `DesktopGuardProUI` 值；不同路径与类型的条目保留。界面开关
直接使用当前用户库，安装和升级不主动启用主界面自启动。

状态转换通过 `Coordinator.TransitionPersistedFrom` 核对会话 ID、修订和状态。
暂停恢复先持久化活动状态，让控制器启动；确认超时后使用独立、有期限的上下文回退，
回退只作用于本次操作的修订。仅用户会话活动模式保留写入器，允许服务端采集器列表为空。

代理活动策略和汇总携带会话及修订，活动禁用、策略改变或代次改变时清理旧汇总。
局部报告从同一份完整性已验证的快照读取指定证据及最新人工处置状态。

## 构建和回归

在项目根目录通过 `go -C backend` 执行 Go 命令，或进入 `backend/` 后直接
使用 `go`。发布工具从相邻的 `frontend/`、`assets/` 及根目录读取构建输入。

```powershell
go -C backend test ./... -count=1 -timeout=120s
go -C backend vet ./...
go -C backend run ./cmd/desktop-guard-release `
  --version 2.11.44 --output ../dist/staging
```

单个包回归示例：

```powershell
go -C backend test ./internal/service -count=1
go -C backend test ./cmd/desktop-guard-release -count=1
```

`TestReleaseInputsExistFromBackendModule` 验证原生工程、图标和许可文件可读取。
钩子 callback 测试通过 Windows ABI 传递模拟输入结构；指针检查和 race
使用 [项目上下文](../../PROJECT_CONTEXT.md) 中的专门命令。

2.11.40 的 MSI 事务允许指定旧测试证书单向迁移到当前 Raymond 证书。
迁移规则位于 `maintenance/msi_transaction_windows.go`，并保留所有者、活动会话、
事务内签名者、备份和回滚检查。

2.11.42 的 DeskGuard 验证态只向获得前台焦点的密码框放行受限键盘输入，
并阻断所有鼠标及桌面快捷键。临时控制验证成功后，服务保留 stopping 状态，
直到代理卸载钩子并确认停止。
