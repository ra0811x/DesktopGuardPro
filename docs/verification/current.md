# Desktop Guard Pro 最新验证记录

本记录核对 2026 年 9 月 27 日的 `2.11.39` 源码与发布物。目录规范化、
键鼠回调指针修复及全部自动化检查通过；四个程序与 MSI 已使用 Raymond
证书签名并验证。2.11.38 MSI、回执和源码 ZIP 保持原始内容。

## 保留的目录与文档

有效源码和交付说明使用稳定入口，生成输出由工具链按需创建。

- `backend/`：Go 模块、服务、代理、维护程序、发布工具和共享业务模块。
- `frontend/`：WinUI 3 原生界面及原生状态测试。
- `docs/`：模块说明、技术架构与操作、验收矩阵、发布说明和本记录。
- `releases/2.11.39/`：当前 MSI、构建回执、对应源码 ZIP、公开证书和校验清单。
- `releases/2.11.38/`：未覆盖的原始发布物。
- `assets/`、`installer/`、`scripts/`：资源、WiX 定义与七个有效 PowerShell 工具。
- 根目录保留 README、AGENTS、PROJECT_CONTEXT、PROJECT_STATUS、忽略规则和许可。

项目入口见 [项目上下文](../../PROJECT_CONTEXT.md)，运行及验收边界见
[项目状态](../../PROJECT_STATUS.md)。实际运行数据仍位于
`%ProgramData%\DesktopGuardPro`，本轮未操作该目录或已安装服务。

## 清理结果

清理分为既有无用产物移除和验证后生成输出移除。文件移入 Windows 回收站，
项目目录内不留额外备份，回收站未清空。

| 类别 | 结果 |
| --- | --- |
| Vue/Wails 历史界面 | 删除旧 frontend 及 Go UI 入口，Go 模块依赖执行 tidy。 |
| 三版历史发布 | 删除 2.11.35、2.11.36、2.11.37 的九份 MSI、回执和源码包。 |
| 重复预览和诊断 | 删除旧 staging、ui-preview、ui-review、module-audit、DeskGuard 检查和旧离线文档。 |
| 失效工具 | 删除五个依赖已不存在审查报告的 Python 更新脚本。 |
| 编译和依赖缓存 | 移除旧 node_modules、bin、obj，验证后再次移除新生成的 bin、obj 和 dist。 |
| 第一轮清理统计 | 8,879 个文件，2,260,301,771 字节，约 2.11 GiB。 |
| 验证后输出清理 | 1,384 个新生成文件移出项目。 |
| 整理后的项目 | 约 108 MiB，不计 `.git/`；其中当前发布物约 104 MiB。 |

`.git/`、此前的文档和钩子修复保留。对迁移前文件逐个核对后，
258 个保留文件内容哈希一致，11 个文件按迁移计划修改，61 个旧界面和历史工具
源文件按确认范围移除，没有发现未计划的内容变化或丢失。

## 构建路径修复

Go 模块整体移入 backend，模块名保持 `desktopguardpro`，包导入不变。
目录相关修改只作用于发布输入、测试和脚本，不改变保护会话或采集协议。

- 原生工程路径调整为相邻的 `../frontend/DesktopGuardPro.Native`。
- 发布图标和许可文件从模块的父目录读取。
- Go 命令从根目录使用 `go -C backend`，MSI 默认输出到 `releases/<版本>/`。
- 原生界面检查脚本读取新的 frontend 工程。

新增的 [发布输入回归](../../backend/cmd/desktop-guard-release/main_test.go)
先复现了迁移后找不到图标的问题，修复后验证原生工程、图标和许可文件可读取。
本轮实际执行发布工具，成功构建 2.11.39 服务、原生 UI、代理和维护程序，
签名四组件后定稿清单，再构建并签名 MSI。安装包和公开证书移入发布目录，
临时构建输出在交付检查后清理。

目录迁移还可以保留根目录 Go 模块并修改所有包路径；本轮采用整体迁移模块，
使既有导入路径和内部包边界保持稳定。对应回归覆盖真实文件读取和完整组件构建。

## 自动化结果

下列命令在新目录中执行，均完成且通过。

| 检查 | 结果 |
| --- | --- |
| `go -C backend mod verify` | 所有模块校验通过。 |
| `go -C backend test ./... -count=1 -timeout=120s` | 全量 Go 测试通过。 |
| `go -C backend vet ./...` | 无告警。 |
| `go -C backend test ./internal/deskguard/hook -count=1 -gcflags=all=-d=checkptr=2` | 钩子及 Windows callback ABI 回归通过。 |
| `go -C backend test -race ./internal/deskguard/hook ./internal/agent ./internal/service -count=1 -timeout=120s` | 三个包通过。 |
| `dotnet run --project frontend/DesktopGuardPro.Native.Tests -c Release` | 历史选择、旧响应、分页、范围、模式草稿和输入策略测试通过。 |
| `scripts/test-native-ui-shell.ps1` | 原生界面结构和跨页接线通过。 |
| 发布工具完整构建 | 2.11.39 三个 Go 程序编译、WinUI Release x64 发布及清单定稿通过。 |
| `scripts/test-msi-release.ps1` | 签名门禁、发布顺序、回执和拒绝发布不可信签名通过。 |
| `scripts/test-msi-package.ps1` | WiX/ICE、界面、事务顺序、四组件和运行依赖检查通过。 |
| 实际 MSI 只读核对 | ProductVersion 为 2.11.39，UI 文件版本为 2.11.39.0，四组件、原生运行文件、清单和许可齐全。 |
| 实际组件和 MSI 签名 | 五个文件签名为 Raymond，均为 Valid 且附带时间戳；组件哈希、清单签名身份和 MSI 构建回执一致。 |
| 原生 EXE 只读核对 | 文件版本、产品版本、PerMonitorV2 清单和运行文件条目通过；未启动界面。 |
| Markdown 本地链接 | 全部有效。 |
| 保留文件与发布物哈希 | 迁移核对通过。 |

钩子回归位于 [callback_windows_test.go](../../backend/internal/deskguard/hook/callback_windows_test.go)，
覆盖物理按下和抬起、鼠标点击和移动、滚轮、注入输入、验证期间放行、热键解锁
及非动作消息转发；不安装真实键鼠钩子。旧代码的键盘、鼠标用例均能触发
checkptr 错误，指针类型修复后通过。

## 发布物一致性

当前 MSI SHA-256 为
`C1CF82F615810466CD6A138660EEDF35C288AF01EEFBAC2E1D9E2D0C9EE7CE0C`，
签名者为 `CN=Raymond`，证书指纹为
`DC38087948CDD021FD794F0D7B6B4F5A12345AEE`。
证书有效期为 2026 年 9 月 27 日至 2029 年 9 月 27 日，证书 SHA-256 为
`670117E1BD2C3DC9A61724A3176E88CB0D6B608E37360E094881770C0813EF71`。
私钥设为不可导出，保存在 `CurrentUser/My`；发布只包含公开证书。

MSI 首次签名遇到时间戳服务器响应失败，重试后完成 DigiCert SHA-256
时间戳及 `signtool verify /pa /all` 核对，没有上传失败产物。

以下 2.11.38 文件移动前后 SHA-256 一致。

| 文件 | SHA-256 |
| --- | --- |
| `DesktopGuardPro-2.11.38-windows-amd64.msi` | `871D056E743AD3D78BC40E0DE60E91D3D5A881E49131FC827AAD8776C8C764B8` |
| `DesktopGuardPro-2.11.38-windows-amd64.msi.build.json` | `8BD59578A2AD44F7A39DB5736B7707478021A8E4FF55990D4BE1C07D1EE4E74E` |
| `DesktopGuardPro-2.11.38-source.zip` | `CE6AC1E5D1FD0906D235DE0E5D878A8DF31346DF4F84B82C4256399009C03BB2` |

2.11.38 源码 ZIP 保留该版本原始源码和目录，不包含后续修复与迁移，
原始构建回执未改动。2.11.39 源码 ZIP 从对应 Git 标签导出，使用新目录。
本轮未安装 MSI、启用真实键鼠钩子或修改已安装服务与运行数据。

## 安装兼容边界

维护程序和 MSI 事务核对安装清单中的签名身份。旧测试签名与 Raymond 签名
不同，原位升级会返回 `ErrUpgradePublisherMismatch`；保留数据卸载会保留清单，
也会保留该限制。此轮保持发布者一致性门禁，既有数据迁移需单独处理。

## 实机验收边界

自动化检查不覆盖以下设备操作，继续按实际证据记录验收结果。

- 七页原生操作、风险证据跳转和报告保存对话框。
- 真实物理键鼠锁定、远程输入和密码窗口交互。
- 目标设备安装升级、Windows 10/11、睡眠恢复、磁盘满和安全日志清理。

完整条件见 [需求验收矩阵](../requirements-acceptance-matrix.md)。
