# Desktop Guard Pro 最新验证记录

2026 年 10 月 8 日核对工作区六项修复，基准提交为 `927d0a1`。
六项修复纳入 `2.11.42` 发布包；`2.11.41` 及历史发布文件保持原样。
审查和修复实现见 [源码审查记录](../reviews/code-review-2026-10-08.md)。

## 修复验证

六个复现场景先在修改前失败，再转为正式回归。修改后对应场景通过，边界检查包含
失败回退、并发修订、过期活动、文件清单篡改和完整回滚。

| 场景 | 结果与证据 |
| --- | --- |
| 暂停后恢复 | 真实协调器与采集控制器能完成恢复；超时返回暂停，记录恢复和回退事件。 |
| 会话及修订变化 | 原生请求与服务持久化核对目标；旧恢复不覆盖并发修订，旧输入验证不结束替代会话。 |
| 基线转入待确认 | 绑定准备修订的启动请求仍能返回刚完成的待确认结果，未绕过用户决定。 |
| 仅用户会话活动 | 空服务端采集器列表保留写入器，模拟代理观察写入且停止时排空。 |
| 完整运行文件 | 部署复制主程序集、配置、嵌套和空资源；运行文件摘要变化在安装前被拒绝。 |
| 升级与回滚 | 更新旧 DLL、新增资源、移除已登记旧资源；健康检查失败恢复旧文件集和发布清单。未登记文件保留。 |
| 旧安装清单 | 缺少 runtimeFiles 的既有清单继续加载；新清单能检测原生 DLL 篡改。 |
| MSI 运行文件 | 安装验证检测旧 DLL；打包脚本在 WiX 执行前拒绝摘要不匹配的运行文件。 |
| 活动归属 | 禁用、会话切换和同会话修订改变都不携带旧窗口或计数；服务拒绝旧修订上报。 |
| 报告路径 | processImage 在对象省略及仅名称选项下裁剪，完整路径选项保留信息；嵌套结构通过。 |
| 局部报告状态 | 单事件报告保留最新人工状态；状态位于范围外时仍验证完整性，篡改继续拒绝导出。 |

正式回归位于对应的 `internal/service`、`collector`、`agent`、`maintenance`、
`reporting` 和 `storage` 模块；发布清单回归位于 `cmd/desktop-guard-release`。

## 全量检查

以下检查针对本轮工作区。键鼠验证使用模拟输入，安装与升级验证使用夹具和模拟系统依赖。

| 检查 | 结果 |
| --- | --- |
| Go 全包回归 | 通过；排除 TestAcquireInteractiveAgentInstanceIsExclusiveAndRecoverable。 |
| Go vet | 通过。 |
| Go 模块校验 | all modules verified。 |
| race | hook、agent、service、collector、storage、maintenance 通过。 |
| 钩子 checkptr | -gcflags=all=-d=checkptr=2 通过。 |
| Native.Tests | 历史选择、旧响应、分页、报告范围、模式草稿和临时控制策略通过。 |
| 原生界面结构 | test-native-ui-shell.ps1 通过。 |
| WinUI Release 构建 | 成功，零警告、零错误。 |
| 四组件开发构建 | 发布工具完成原生自包含发布和三个 Go 组件构建；四组件摘要、505 条运行文件记录及 510 个 ZIP 条目一致。 |
| MSI 发布门禁 | test-msi-release.ps1 通过，包括运行文件篡改拒绝。 |
| MSI 表结构 | test-msi-package.ps1 通过，WiX/ICE 无告警；未安装夹具 MSI。 |
| 工作区差异 | git diff --check 通过。 |

单实例用例使用产品级互斥体，可能被已安装代理持有。本轮按此前记录排除该用例，
没有停止已安装代理来取得测试所有权。

实际 Go 检查命令为：

```powershell
go -C backend test ./... -count=1 -timeout=120s `
  -skip TestAcquireInteractiveAgentInstanceIsExclusiveAndRecoverable
go -C backend vet ./...
go -C backend mod verify
go -C backend test -race ./internal/deskguard/hook ./internal/agent `
  ./internal/service ./internal/collector ./internal/storage ./internal/maintenance `
  -count=1 -timeout=120s `
  -skip TestAcquireInteractiveAgentInstanceIsExclusiveAndRecoverable
go -C backend test ./internal/deskguard/hook -count=1 -gcflags=all=-d=checkptr=2
```

## 交付边界

本轮没有安装 MSI、启动真实控制钩子或操作运行数据；发布组件与 MSI 已签名。
既有 AGENTS.md 删除状态保留。真实键鼠、窗口交互和目标设备安装升级仍需实机验收。

开发输出位于 `dist/fix-validation-20261008/`，前一轮临时复现位于
`dist/code-review/`。已核对清理目标位于当前工作区的专用 dist 子目录。
清理被自动审批以 `blocked by policy` 拒绝，两个目录暂留；正式回归位于源码模块中。

## 2.11.42 发布验证

2026 年 10 月 8 日从本轮源码重新构建 `2.11.42`，四个程序和 MSI 使用本机
Raymond 证书签名并附带时间戳。构建在 `dist/release-2.11.42/` 完成，交付文件
保存到新的 `releases/2.11.42/`，没有覆盖历史安装包。

| 检查 | 结果 |
| --- | --- |
| 发布前回归 | Go 全包回归（同一单实例用例排除）、vet、mod verify、Native.Tests、原生结构、MSI 门禁及表结构重新通过。 |
| 四组件签名 | Valid，CN=Raymond，指纹 DC38087948CDD021FD794F0D7B6B4F5A12345AEE，均附时间戳。 |
| 原生版本与清单 | 产品版本 2.11.42，文件版本 2.11.42.0；应用清单无重复运行文件条目。只提取清单，未启动界面。 |
| 实际 MSI | ProductVersion=2.11.42，510 个文件；WiX 链接成功、签名 Valid、时间戳存在。 |
| MSI 回执 | 产品版本、签名指纹、六个完成步骤及 SHA-256 与实际 MSI 一致。 |
| 只读解包核对 | 509 个组件及运行文件与签名构建一致；内嵌 release-manifest.json 核对四组件及 505 个运行文件。打包时重新定稿时间，因此不要求清单时间戳与打包前相同。 |
| MSI 界面引用 | 解包工具出现 DARK1059 提示；直接查询实际 Control/ControlEvent 表，211 个控件、缺失控件引用为零。 |
| 发布附件 | MSI、构建回执、对应标签源码 ZIP、公开证书及 SHA256SUMS.txt。私钥没有导出。 |

MSI SHA-256：
`BA14F59091E5F44C1122F1D5DC9C5D00B8B178EDE8D5E930AB962BB0B71E1C03`。
发布说明见 [2.11.42](../releases/2.11.42.md)。

发布构建使用当前用户证书库验证签名。本轮没有重新执行 SYSTEM 账户验证；此前机器
信任记录见项目状态。目标设备安装升级、真实键鼠与窗口交互仍保留实机验收边界。

交付包检查完成后，已核对本次清理目标为工作区专用目录 `dist/release-2.11.42/`。
自动审批以 `blocked by policy` 拒绝清理，目录暂留，包含签名暂存及只读解包结果。
