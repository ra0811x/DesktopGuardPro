# 前端模块说明

`frontend/` 保存 .NET 8 与 WinUI 3 原生界面及其状态回归工程。
当前项目已删除退出发布链的 Vue/Wails 界面，不需要 Node.js 或 npm。

## 工程与入口

界面逻辑、跨页状态和命名管道访问分开实现。

| 文件 | 职责 |
| --- | --- |
| [DesktopGuardPro.Native.csproj](../../frontend/DesktopGuardPro.Native/DesktopGuardPro.Native.csproj) | 原生工程、目标平台、运行依赖和图标资源。 |
| [App.xaml](../../frontend/DesktopGuardPro.Native/App.xaml) | 主题、控件样式和资源。 |
| [App.xaml.cs](../../frontend/DesktopGuardPro.Native/App.xaml.cs) | 窗口、菜单、七页布局、仪表盘和操作响应。 |
| [App.Analysis.cs](../../frontend/DesktopGuardPro.Native/App.Analysis.cs) | 会话选择、分析导航、证据跳转和异步请求校验。 |
| [AnalysisWorkspace.cs](../../frontend/DesktopGuardPro.Native/AnalysisWorkspace.cs) | 所选会话、修订号、分页、报告范围和模式草稿。 |
| [ControlPipeClient.cs](../../frontend/DesktopGuardPro.Native/ControlPipeClient.cs) | 命名管道、请求超时和报告分块校验。 |
| [ControlMessage.cs](../../frontend/DesktopGuardPro.Native/ControlMessage.cs) | 服务消息和原生数据模型。 |
| [WindowsCredentialPrompt.cs](../../frontend/DesktopGuardPro.Native/WindowsCredentialPrompt.cs) | 保护结束时的 Windows 身份确认。 |
| [NativeTrayIcon.cs](../../frontend/DesktopGuardPro.Native/NativeTrayIcon.cs) | 系统托盘。 |

## 页面关联

七个页面共享所选分析会话，仪表盘保护任务与历史分析对象分别维护。

- 仪表盘：模式、保护生命周期、监控范围和临时键鼠锁定。
- 历史会话：会话选择和保留锁。
- 审计结果：事件筛选、分页、详情和单事件报告范围。
- 风险分析：评估、证据跳转和人工处置状态。
- 资产差异：会话开始与结束的清单比较。
- 报告导出：格式、序号范围、隐私字段和本地文件保存。
- 系统设置：各监控模式的模块、详情、保存和重置。

切换会话或筛选时重置分页，响应写入页面前验证会话修订和请求序号。
设置草稿在本次应用运行期间按模式隔离，关闭前需要保存。
完整按钮行为见 [技术架构与操作说明](../technical-architecture.md)。

保护状态请求携带当前 `SessionInfo` 的 ID 和修订，服务拒绝过期操作。
准备期间基线刚转入待确认状态时，启动请求返回待确认会话，界面继续显示失败项处理流程。
报告的序号范围限定证据，人工处置状态采用该次报告数据快照中的最新状态。

## 构建和回归

从根目录执行原生状态测试与 Release 构建。

```powershell
dotnet run --project frontend/DesktopGuardPro.Native.Tests -c Release
dotnet build frontend/DesktopGuardPro.Native/DesktopGuardPro.Native.csproj `
  -c Release -p:Platform=x64
.\scripts\test-native-ui-shell.ps1
```

[原生状态测试](../../frontend/DesktopGuardPro.Native.Tests/Program.cs) 不依赖界面启动，
覆盖历史选择、旧响应、分页、报告区间、模式草稿和 DeskGuard 输入策略。
`test-native-ui-release.ps1` 是会启动真实界面的发布检查，需显式传入可执行文件。
窗口、保存对话框和实际键鼠控制仍以目标设备验收为准。

2.11.41 的临时控制提示与钩子行为一致：验证窗口打开时只允许密码框接收键盘，
取消后恢复锁定，成功后等待代理完成钩子停止确认。
