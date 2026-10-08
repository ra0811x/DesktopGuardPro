# Desktop Guard Pro 源码审查

2026 年 10 月 8 日审查工作区源码，基准提交为 `927d0a1`。
覆盖后台服务、采集与存储、交互式代理、WinUI 界面、维护命令和 MSI 发布路径。
确认六处缺陷，其中三处为 P1，三处为 P2。六处已修复并纳入 2.11.42 发布源码。
各项问题描述记录修改前的行为，随后给出修复实现；原始临时测试名用于对应修改前证据。

## 1. P1：暂停后恢复保护会超时

暂停稳定后，采集管线已经停止。界面请求 `paused -> active` 时，服务先调用
`WaitForActive`，等它成功后才执行状态转换。采集控制器只为 `preparing`、
`active`、`degraded` 启动管线，两端形成循环等待。

- 服务等待位置：[`api.go`](../../backend/internal/service/api.go)。
- 状态转换位置：[`api.go`](../../backend/internal/service/api.go)。
- 采集启动条件：[`session_controller.go`](../../backend/internal/collector/session_controller.go)。
- 界面调用：[`ToggleProtectionPauseAsync`](../../frontend/DesktopGuardPro.Native/App.xaml.cs)。

`TestReviewResumePausedWithRealController` 使用真实协调器、真实采集控制器和模拟采集器。
将请求期限缩短至 100 ms 后，返回 `invalid_request`，会话仍为 `paused`。
界面常规请求的期限为 30 秒，重复点击也不会恢复采集。

修复实现：恢复先持久化活动状态，再确认管线启动。超时使用独立、有期限的上下文回退，
回退核对本次会话与修订。原生请求携带会话及修订，基线异步转入待确认时返回待确认对象。

## 2. P1：只启用用户会话活动会让采集控制器退出

自定义模式只启用用户会话活动时，策略验证通过。该模块由代理上报，服务端工厂返回
空采集器列表。管线创建仍要求 `Supervisor` 至少有一个采集器，因此启动失败。

- 工厂返回：[`directory_scope.go`](../../backend/internal/collector/directory_scope.go)。
- 管线初始化：[`session_controller.go`](../../backend/internal/collector/session_controller.go)。
- 空列表拒绝：[`supervisor.go`](../../backend/internal/collector/supervisor.go)。
- 控制器失败后退出：[`session_controller.go`](../../backend/internal/collector/session_controller.go)。

`TestReviewUserActivityOnlySessionCanRun` 先验证策略合法，再运行真实控制器。
结果为 `create collector pipeline ...: at least one collector is required`。
主服务进程继续提供接口，但采集控制器不再运行，该模式无法启动保护。
仅启用文件模块但未配置目标，也可能形成相同的空列表。

修复实现：服务端采集器为空时保留写入器，启动确认等待写入器就绪。正式回归同时验证
代理观察可写入并在停止时完成排空，监督器对空列表的原有校验保留。

## 3. P1：独立部署和升级遗漏 WinUI 程序集与资源

原生界面采用自包含发布，界面业务代码在 `desktop-guard-ui.dll`，并依赖其他 DLL、
运行配置和资源目录。独立 `deploy`、`upgrade` 命令仍只复制或替换四个 EXE。

- 部署文件列表：[`deployer_windows.go`](../../backend/internal/maintenance/deployer_windows.go)。
- 升级替换列表：[`upgrader_windows.go`](../../backend/internal/maintenance/upgrader_windows.go)。
- 原生工程：[`DesktopGuardPro.Native.csproj`](../../frontend/DesktopGuardPro.Native/DesktopGuardPro.Native.csproj)。
- MSI 完整运行文件收集：[`build-msi.ps1`](../../scripts/build-msi.ps1)。

`TestReviewDeployIncludesNativeUIAssembly` 在模拟发布目录中加入界面 DLL，再执行真实
部署流程，签名检查与系统安装动作使用模拟依赖。部署返回成功，但目标目录没有该 DLL。
独立升级也不会替换安装目录中的界面 DLL，因此界面代码可能停留在旧版本。
MSI 路径已收集原生运行文件，本问题定位于独立维护命令。

修复实现：发布和安装清单增加完整运行文件登记，部署校验复制结果，升级将替换、新增
和旧清单登记资源的移除纳入事务。失败恢复文件集及发布清单。旧清单无登记时保持兼容，
未登记文件保留；MSI 验证、卸载计划和重新安装时的待删除操作取消同步覆盖运行文件。

## 4. P2：活动汇总跨暂停及会话边界保留

代理的 `activityState` 在运行循环外创建。活动暂停或结束后，只停止采样，未清空
旧窗口、计数和起止时间。恢复活动时，未上报的旧汇总继续使用；实际管道上报器再查询
当前会话 ID，将该汇总归入当时的会话。

- 汇总状态生命周期：[`runtime.go`](../../backend/internal/agent/runtime.go)。
- 活动启用条件：[`runtime.go`](../../backend/internal/agent/runtime.go)。
- 上报时获取会话 ID：[`runtime_windows.go`](../../backend/internal/agent/runtime_windows.go)。

`TestReviewActivityDoesNotCrossDisabledBoundary` 模拟 A 会话积累 7 次键盘活动、
活动禁用、B 会话启用并切换窗口。B 的首次上报仍包含 A 的窗口标题和 7 次计数，
前台时间还覆盖了禁用间隔。该测试使用模拟来源与上报器，没有启动真实输入钩子。

修复实现：采样策略和汇总携带会话与修订，上报保留原归属。活动禁用、会话、修订或
采样字段策略变化时清理旧汇总，并丢弃禁用期间积累的输入转换；服务再次核对上报修订。

## 5. P2：报告负载中的前台进程路径未脱敏

报告选择“仅对象名称”、包含负载、关闭用户名时，脱敏器只识别以 `path` 结尾的键
及 `objectName`。代理真实活动负载采用 `processImage` 保存可执行文件完整路径，
该字段不会经过路径脱敏。

- 脱敏分支：[`report.go`](../../backend/internal/reporting/report.go)。
- 生产负载字段：[`runtime.go`](../../backend/cmd/desktop-guard-service/runtime.go)。

`TestReviewPayloadRedactsForegroundProcessImage` 输入
`C:\Users\Alice\PrivateTools\app.exe`，输出仍保留完整路径；同一负载的窗口标题
能够按设置被隐藏。用户名和目录信息会随报告外发，与对象名称选项不一致。

修复实现：路径脱敏覆盖 `processImage`，包括数组和对象中的嵌套字段。正式回归验证
省略、仅名称及完整路径三个选项，完整路径选项仍保留原信息。

## 6. P2：导出单事件时丢失已保存的风险处置状态

人工状态通过后续 `risk_finding_status_changed` 事件保存。报告先截取序号范围，
再仅从这些记录中恢复状态；状态变更事件通常位于证据事件之后，因此单事件导出遗漏
处置记录。同一个风险 ID 在界面显示“已知”，在局部报告中变成“待确认”。

- 导出范围评估：[`analysis_api.go`](../../backend/internal/service/analysis_api.go)。
- 从局部记录恢复状态：[`analysis_api.go`](../../backend/internal/service/analysis_api.go)。
- 界面的单事件导出入口：[`App.Analysis.cs`](../../frontend/DesktopGuardPro.Native/App.Analysis.cs)。

`TestReviewPartialReportPreservesKnownStatus` 先将设备风险设为 `known`，随后只导出
其第 1 条证据事件。报告返回同一风险 ID，但状态为 `pending_review`。

修复实现：局部报告采用本次数据快照的最新人工处置状态。存储在同一份验证快照中读取
指定证据和状态事件，仅解密所选证据与状态事件；范围外的状态记录不加入报告时间线。

## 设计与测试覆盖

六处缺陷涉及跨模块契约。模块单测通过后，还需要验证真实模块之间的组合行为。

1. 原有生命周期替身和“四个组件”夹具未覆盖跨模块组合。本轮新增真实控制器回归，
   维护夹具包含原生 DLL、嵌套资源和失败回滚，原有模块回归同时保留。
2. 状态请求、异步等待、凭据验证与持久化现在核对目标会话及修订，
   恢复回退和替代会话隔离均有正式回归。旧协议调用保留兼容处理。
3. `App.xaml.cs` 同时维护七页布局、网络调用、草稿、凭据和文件保存；
   `service/api.go` 混合多个领域的协议、授权和生命周期逻辑。
   后续功能修改可按已有页面和服务职责拆分，保持接口与运行行为一致。

## 验证与边界

修改前六个补充测试均失败。正式回归已保存到各模块，修改后六个场景及超时、并发、
清单篡改、完整回滚和范围外状态完整性检查通过。详细执行结果见最新验证记录。

| 检查 | 结果 |
| --- | --- |
| Go 全包回归 | 通过；排除已有代理可能占用的单实例互斥体用例。 |
| Go vet | 通过。 |
| hook、agent、service 的 race 回归 | 通过；同样排除互斥体环境用例。 |
| Native.Tests | 通过。 |
| 原生界面结构检查 | 通过。 |
| MSI 签名门禁与发布顺序模拟 | 通过。 |
| WiX/ICE 与安装表结构检查 | 通过，未安装生成的夹具 MSI。 |
| 六个缺陷正式回归 | 修复后全部通过，原失败信息作为修改前证据保留。 |
| WinRT 报告保存编码探针 | 新文件及覆盖 UTF-16 旧文件均保存为预期 UTF-8 字节，未确认编码缺陷。 |

实际执行的 Go 基线命令为：

```powershell
go -C backend test ./... -count=1 -timeout=120s `
  -skip TestAcquireInteractiveAgentInstanceIsExclusiveAndRecoverable
go -C backend vet ./...
go -C backend test -race ./internal/deskguard/hook ./internal/agent `
  ./internal/service -count=1 -timeout=120s `
  -skip TestAcquireInteractiveAgentInstanceIsExclusiveAndRecoverable
```

审查与修复验证未运行真实物理键鼠控制、未安装 MSI、未改动运行数据。
本记录包含源码审查和修复结果；2.11.42 发布验证见最新验证记录，实机验收边界继续保留。
本轮 `dist/code-review/` 保留补充复现源码、overlay 和 WinRT 编码探针。
正式回归已移入对应源码模块；开发输出位于 `dist/fix-validation-20261008/`。
两轮临时输出清理被自动审批拒绝，返回 `blocked by policy`，暂未删除。
