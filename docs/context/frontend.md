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
| [App.Settings.cs](../../frontend/DesktopGuardPro.Native/App.Settings.cs) | 分级菜单、偏好切换、关闭与托盘行为。 |
| [UiPreferences.cs](../../frontend/DesktopGuardPro.Native/UiPreferences.cs) | 用户偏好持久化、自启动命令和可模拟的启动项接口。 |
| [UiStartupRegistry.cs](../../frontend/DesktopGuardPro.Native/UiStartupRegistry.cs) | 当前用户主界面的登录启动注册表项。 |
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

顶部 **设置** 提供四组子菜单，普通与紧凑布局复用同一功能构造器并同步开关。
主界面自启动使用 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 的
`DesktopGuardProUI` 值，命令为带引号的完整程序路径及 `--autostart`。
其他偏好保存到 `%LocalAppData%\DesktopGuardPro\ui-preferences.json`，保存失败时
保留旧值并提示。默认报告格式只影响格式，不自动开启任何敏感字段。

默认普通启动显示窗口、关闭退出、每 5 秒刷新状态。可单独启用自启动时收起或
关闭时收起到托盘；托盘不可用时不隐藏。单击或双击托盘图标恢复窗口，右键菜单
提供打开与退出；Explorer 重启后重建图标。文件菜单与托盘的显式退出忽略收起偏好。
Windows 启动应用管理可另行禁用启动项，菜单提供该系统页面的入口。

**开机自启动（登录后）** 在当前用户进入桌面后运行，后台服务在系统启动时独立
自动启动。界面初始化先尝试连接服务，失败每 5 秒重试，最多六次；成功后读取
目录、模式和输入管理数据，普通状态轮询不重新覆盖可编辑配置。重试期间退出
界面会取消等待，关闭自动刷新偏好不取消本次有上限的初始化重试。

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
覆盖历史选择、旧响应、分页、报告区间、模式草稿、DeskGuard 输入策略，
以及自启动命令、注册失败、偏好保存与文件锁冲突、托盘安全条件和显式退出。
`test-native-ui-release.ps1` 是会启动真实界面的发布检查，需显式传入可执行文件。
窗口、保存对话框和实际键鼠控制仍以目标设备验收为准。

2.11.41 的临时控制提示与钩子行为一致：验证窗口打开时只允许密码框接收键盘，
取消后恢复锁定，成功后等待代理完成钩子停止确认。
