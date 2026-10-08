# Desktop Guard Pro

Desktop Guard Pro 是面向 Windows x64 的本机保护与审计应用。它通过后台服务、
用户会话代理、原生桌面界面和维护程序，记录保护期间的文件、进程、软件、设备、
账户和系统配置变化，并提供风险分析、完整性校验和报告导出。

本文按 2026 年 10 月 8 日发布的 `2.11.44` 和源码核对。
当前四个程序及安装包的签名者为 `Raymond`。

详细的设计意图、进程架构、七页按钮与接口、配置生效条件和使用流程图，见
[技术架构与完整操作说明](docs/technical-architecture.md)。

开发定位见 [项目上下文](PROJECT_CONTEXT.md)、[项目状态](PROJECT_STATUS.md)、
[前端说明](docs/context/frontend.md) 和 [后端说明](docs/context/backend.md)。
工作区检查结果统一保存在 [最新验证记录](docs/verification/current.md)。

## 当前版本

当前版本为 `2.11.44`，安装包位于 `releases/2.11.44`。本版新增分级设置、
界面开机自启动、托盘偏好和启动连接重试，保留此前六项修复。

- 安装包：
  [`DesktopGuardPro-2.11.44-windows-amd64.msi`](releases/2.11.44/DesktopGuardPro-2.11.44-windows-amd64.msi)
- 构建回执：
  [`DesktopGuardPro-2.11.44-windows-amd64.msi.build.json`](releases/2.11.44/DesktopGuardPro-2.11.44-windows-amd64.msi.build.json)
- 对应发布源码：
  [`DesktopGuardPro-2.11.44-source.zip`](releases/2.11.44/DesktopGuardPro-2.11.44-source.zip)
- 公开证书：[`Raymond-code-signing.cer`](releases/2.11.44/Raymond-code-signing.cer)
- 文件校验：[`SHA256SUMS.txt`](releases/2.11.44/SHA256SUMS.txt)
- 发布说明：[2.11.44](docs/releases/2.11.44.md)
- 当前公开版本：[GitHub Release 2.11.44](https://github.com/ra0811x/DesktopGuardPro/releases/tag/v2.11.44)
- SHA-256：
  `CE6F89487F5BBCE2B547B550476E4D71AC346D7E492880037E55E90127478570`
- 签名者：`CN=Raymond`
- 证书指纹：`DC38087948CDD021FD794F0D7B6B4F5A12345AEE`

2.11.44 保留既有解锁行为和指定证书迁移规则。修复范围和验证结果见
[发布说明](docs/releases/2.11.44.md)。

## 产品能力

工作区验收矩阵保留 FR-001 至 FR-097 共 72 条历史功能需求，并记录七个模块与
`2.11.38` 临时输入控制的验证结果。涉及 Windows 版本、管理员权限、硬件和异常
恢复的场景仍需在目标设备验证。详细条件见
[`docs/requirements-acceptance-matrix.md`](docs/requirements-acceptance-matrix.md)。

主要能力包括：

- 创建、命名、启动、暂停、恢复、结束保护会话，并在服务重启后识别未完成会话。
- 每次启动从宽松、标准、严格和自定义模式中选择，预览本次功能、权限和资源影响。
- 在 **系统设置** 分别查看、编辑、保存或重置四种模式的模块和详细审计策略。
- 独立锁定本地键盘和鼠标，可定时释放，也可使用本地密码或恢复码解锁。
- 配置本地文件、文件夹和可移动卷，并为每条目录规则设置递归状态。
- 按路径、文件名、扩展名和进程维护排除规则。
- 记录文件创建、写入、截断、删除、重命名和内容哈希变化。
- 使用差异扫描、重扫和 NTFS USN Journal 补偿采集缺口。
- 在严格监控模式下接入 Windows 对象访问审核。
- 每 5 分钟采集筛选后的相关进程与软件清单，周期可设为 60–3600 秒。
- 汇总前台应用切换、输入活动强度、鼠标点击和滚轮活动。
- 检测网络、代理、防火墙、账户、远程桌面和审计策略变化。
- 记录 USB 存储和常见即插即用设备的接入、移除及属性。
- 提供高、中、低三级风险结果、证据关联和处置状态。
- 导出 HTML、Markdown 和 JSON 报告，并控制敏感字段范围。
- 使用事件完整性链、检查点和导出校验清单保护审计结果。
- 通过 MSI 完成安装、升级、卸载、回滚和健康检查。

## 原生桌面界面

当前 MSI 使用 WinUI 3 和 .NET 8 构建 `desktop-guard-ui.exe`。运行文件随 MSI
自包含发布，不依赖 WebView2 Runtime。

桌面界面提供以下七个页面：

| 页面 | 主要用途 |
| --- | --- |
| **仪表盘** | 选择保护模式、管理会话与监控目标，并单独启动临时输入控制。 |
| **历史会话** | 浏览已完成、降级、中断或保留锁定的会话。 |
| **审计结果** | 筛选事件时间线并查看原始字段和事件详情。 |
| **风险分析** | 查看风险规则结果、评分、置信度和证据数量。 |
| **资产差异** | 按软件、设备、网络、账户和系统类别查看变化。 |
| **报告导出** | 选择报告格式、对象细节和敏感字段范围。 |
| **系统设置** | 编辑四种模式的五类审计模块与详情。 |

界面采用深蓝侧栏、浅蓝页面画布、白色功能卡片和浅蓝信息容器。顶部第一行显示
软件名称，第二行显示菜单、服务状态和保护状态。窗口使用 Per-Monitor V2 DPI
感知，并在较窄窗口中自动收束导航和双栏内容。

仪表盘以 **电脑保护** 和 **临时锁定键鼠** 两张主任务卡组织高频操作；会话名称、
采集详情、监控范围和服务信息放在可展开区域，下方提供历史、审计与导出快捷入口。
临时控制统一锁定本地键盘和鼠标，规则、设备状态与密码管理集中在
**高级设置与设备** 浮层。其余六页沿用统一的卡片、工具栏和详情区设计，
审计、风险、资产和报告共享所选会话。

顶部菜单包含 **文件**、**编辑**、**查看** 和 **设置**。菜单提供页面跳转、
监控配置、隐私设置入口、刷新和导航栏控制。应用图标用于窗口、安装程序、
桌面快捷方式和系统托盘。

## 保护会话流程

后台服务使用持久化状态机协调基线、采集器、风险分析和收尾操作。会话状态会写入
数据库，服务重启后可以识别未完成会话并记录中断区间。

一次完整流程包括：

1. 在 **仪表盘** 添加监控目标、配置排除规则并输入会话名称。
2. 选择 **宽松**、**标准**、**严格** 或 **自定义**，查看本次功能和权限影响。
3. 启动保护，等待资产清单和 USN 检查点准备完成。
4. 查看顶部保护状态、事件时间线、资产变化和风险结果。
5. 根据需要暂停或恢复保护。
6. 结束保护，完成用户身份验证并等待最终扫描结束。
7. 导出报告和同名 `.verification.json` 校验文件。

准备阶段部分检查失败时，服务会保留失败项和原因。继续或取消的选择会进入审计记录。
服务异常重启后，未结束会话会恢复为降级状态，并记录中断开始与恢复边界。

结束保护时使用 Windows 凭据对话框完成身份确认。当前配置不会切换到要求
Ctrl+Alt+Delete 的安全桌面。

### 模式配置与生效时间

打开 **系统设置**，选择需要编辑的模式。模块总开关控制采集器是否随该模式启动；
每个模块的 **详情** 对话框控制事件类型、采样间隔、风险规则等参数。使用
**保存当前模式** 写入配置，使用 **恢复当前模式默认值** 还原单个模式。

新建会话读取所选模式当时的策略。进行中的会话保留创建时的策略快照。四种模式可以
分别配置；编辑宽松模式不会覆盖标准、严格或自定义模式。文件目标和排除规则仍在
**仪表盘** 维护。

宽松、标准、严格和自定义模式默认都关闭 **用户会话活动**。你仍可在任一模式中
手动开启该模块并配置前台应用、窗口标题、键盘和鼠标计数。模式配置不再包含输入
阻断；键盘和鼠标限制统一由仪表盘的 **临时输入控制** 管理。

### 独立临时输入控制

在 **仪表盘** 的 **临时输入控制** 区域，启动后统一锁定本地键盘和鼠标，
远程注入输入保持可用。首次使用时需设置本地密码，已保存的密码可直接使用。
结束方式可选择 1–480 分钟后自动释放，或持续到验证解锁。默认按
**Ctrl+Alt+Space** 或点击 **解除锁定** 打开密码窗口；密码和恢复码在同一输入框
验证。窗口打开期间只允许密码框接收受限键盘输入，所有鼠标及桌面快捷键继续阻断；
取消后恢复锁定，验证成功或定时到期后释放。
服务或系统重启后不会自动重新锁定。

钩子引擎、解锁状态机、密码窗口及输入法处理复用 DeskGuard 源码，位于
`backend/internal/deskguard`。服务适配层沿用本项目的凭据存储，兼容此前保存的密码。

临时输入控制可以在没有保护会话时运行，不会自动启动文件、网络、USB 或用户活动
审计。它与保护会话并行时，两条生命周期彼此独立。临时控制运行期间，用户会话
活动中的键盘、鼠标和高风险快捷键计数会暂停，前台应用与窗口标题可继续采集。
输入拦截报告不会写入保护会话的审计时间线。详细策略、运行状态、设备清单、本地
密码和恢复码通过仪表盘的 **高级设置与设备** 入口维护。设备清单由会话代理持续
维护，未启动临时控制时也可刷新查看；设置或删除本地密码后，界面会显示明确的
成功或失败反馈。输入控制需要当前用户会话中的代理进程运行。

## 文件、进程和输入采集

采集器组合实时通知、周期快照和资产差异扫描。每条事件保留观测时间、数据
来源、风险级别和可获得的用户或进程关联信息。

文件采集包含以下行为：

- 对常规文件计算 SHA-256 内容哈希。
- 将较大文件放入后台限速队列，并记录排队、完成或失败状态。
- 在目录通知缓冲区溢出时生成采集缺口事件并启动重扫。
- 在 NTFS 卷上读取 USN Journal，补偿服务中断期间的变化。
- 使用开始、结束资产清单比较软件、账户、设备和系统配置变化。

进程与软件模块默认每 5 分钟生成一条相关进程快照。筛选器忽略常规 Windows
组件与稳定服务，保留用户目录、临时目录、名称与路径不一致、异常父进程链、同名
换路径、身份或路径缺失等项目。进程哈希、发布者和签名状态只参与内存分类，不会
写入快照载荷。软件新增、移除和版本变化继续通过软件清单差异记录。

用户会话活动模块默认关闭。手动开启后，默认每 5 秒观察一次，每 30 秒发送一次
汇总；键盘、点击和滚轮只保存计数。窗口标题及高风险组合键类别由模式详情单独
控制。

> **Note:** Windows 文件最后访问时间可能延迟更新或被系统关闭。启用严格读取
> 审计时，程序使用 Windows 对象访问审核事件作为文件读取和打开的主要依据。

## 数据和安全

运行数据默认保存在 `%ProgramData%\DesktopGuardPro`。安装过程会根据安装所有者
SID 配置目录访问控制，桌面界面通过权限受控的本机命名管道访问后台服务。

主要文件包括：

| 文件或记录 | 内容 |
| --- | --- |
| `desktop-guard.db` | 会话、事件、资产基线、风险和保留状态。 |
| `storage.key` | 经 DPAPI 封装的主密钥。 |
| `install-policy.json` | 安装所有者和本机授权策略。 |
| `install-manifest.json` | 组件版本、路径、大小、哈希和签名身份。 |
| `.msi-backup-*` | 安装或升级事务使用的数据与密钥恢复副本。 |
| `maintenance-transaction.json` | 未完成 MSI 事务的恢复记录。 |

事件载荷使用 AES-GCM 加密。事件元数据和密文进入 HMAC 链，服务读取历史事件时
会验证链和检查点。时间线单页最多扫描 4096 条记录，预览限制为 16 KiB，页面
响应预算为 512 KiB。单次报告最多处理 100000 条事件和 32 MiB 原始载荷。

报告导出可以分别控制用户名、对象路径、窗口标题、进程关联键和原始事件负载。
包含敏感字段时，界面要求用户明确确认。每个导出文件都有同名
`.verification.json`，用于核对文件哈希、大小、媒体类型、会话修订和完整性结果。

## 安装和升级

Desktop Guard Pro 支持 Windows 10 版本 1809 或更高版本的 x64 系统。安装、
升级和卸载需要管理员权限。

执行安装时：

1. 结束正在进行的保护会话。
2. 关闭 Desktop Guard Pro 主窗口。
3. 打开
   [`DesktopGuardPro-2.11.44-windows-amd64.msi`](releases/2.11.44/DesktopGuardPro-2.11.44-windows-amd64.msi)。
4. 确认管理员权限和 Raymond 发布者信息。
5. 选择安装目录和桌面快捷方式选项。
6. 等待服务安装、启动和健康检查完成。
7. 启动 Desktop Guard Pro，确认顶部显示 **服务：运行中**。

活动、暂停或降级状态的保护会话会阻止升级与卸载。先正常结束会话，再运行新版
安装包。MSI 卸载默认保留审计数据、策略、密钥和安装清单。维护程序还提供保留
数据、导出后删除和立即删除模式。

## 程序与目录结构

项目包含四个 Windows 程序、共享 Go 模块、原生 WinUI 3 工程和 WiX MSI
安装工程。

| 路径或程序 | 职责 |
| --- | --- |
| `desktop-guard-service.exe` | Windows Service、本机 API、采集和存储。 |
| `desktop-guard-ui.exe` | WinUI 3 原生桌面界面。 |
| `desktop-guard-agent.exe` | 用户会话代理和输入活动汇总。 |
| `desktop-guard-maintenance.exe` | 安装、升级、卸载、验证和诊断。 |
| `frontend/DesktopGuardPro.Native/` | WinUI 界面、清单和主题资源。 |
| `backend/internal/` | 领域模型、采集器、IPC、存储、报告和 Windows 集成。 |
| `backend/cmd/` | 三个 Go 程序和发布工具入口。 |
| `installer/` | WiX MSI 产品定义。 |
| `scripts/` | 发布、签名和安装包检查脚本。 |
| `frontend/` | WinUI 3 原生界面及状态回归工程。 |
| `docs/` | 模块说明、架构操作、需求验收、发布说明和最新验证记录。 |
| `assets/` | 应用图标和品牌资源。 |
| `releases/2.11.44/` | GitHub 当前发布版本的原始发布物。 |
| `releases/2.11.42/` | 历史发布版本的原始发布物。 |
| `releases/2.11.41/` | 历史发布版本的原始发布物。 |
| `releases/2.11.40/` | 历史发布版本的原始发布物。 |

当前发布流程从
`frontend/DesktopGuardPro.Native/DesktopGuardPro.Native.csproj` 生成桌面界面。
已退出发布链的 Vue/Wails 界面已删除；维护程序仍使用的共享模块保留。

## 开发环境

完整构建需要 Windows x64 环境和以下工具链。NuGet 和 Go 依赖需要在首次
构建时可用。

- Go 1.26.0；`backend/go.mod` 指定 Go 1.26.6 工具链。
- .NET 8 SDK。
- Windows App SDK `1.8.260508005`。
- Windows SDK BuildTools `10.0.26100.7705`。
- WiX Toolset 3.14。
- Windows SDK `signtool.exe` 和 `mt.exe`。

## 构建

在项目根目录执行以下命令。普通组件构建可以输出待签名程序，MSI 发布脚本需要
有效的代码签名证书。

构建 Go 组件和发布目录：

```powershell
go -C backend run ./cmd/desktop-guard-release `
  --version 2.11.44 `
  --output ../dist/staging
```

单独构建 WinUI 3 原生界面：

```powershell
dotnet build `
  frontend\DesktopGuardPro.Native\DesktopGuardPro.Native.csproj `
  -c Release `
  -p:Platform=x64
```

生成、签名和校验 MSI：

```powershell
$msiArguments = @{
  Version = '2.11.44'
  OutputDirectory = 'releases\2.11.44'
  CertificateStore = 'CurrentUser'
  CertificateThumbprint = '<certificate-thumbprint>'
}
.\scripts\build-msi.ps1 @msiArguments
```

构建脚本会执行组件构建、四个可执行文件签名、清单定稿、原生 UI 运行文件收集、
WiX 链接、MSI 签名和构建回执写入。目标 MSI 已存在时，脚本会停止，避免覆盖
已经发布的文件。

## 测试

后端、原生界面和安装包都有独立测试入口。以下命令从项目根目录
执行。

运行 Go 检查：

```powershell
go -C backend mod verify
go -C backend test ./...
go -C backend vet ./...
```

运行原生界面结构、清单和启动检查：

```powershell
.\scripts\test-native-ui-shell.ps1

$nativeUiTest = @{
  ExecutablePath = `
    'dist\staging\DesktopGuardPro-2.11.44-windows-amd64\desktop-guard-ui.exe'
  ExpectedVersion = '2.11.44'
}
.\scripts\test-native-ui-release.ps1 @nativeUiTest
```

运行原生状态检查：

```powershell
dotnet run --project frontend/DesktopGuardPro.Native.Tests -c Release
```

运行 MSI 发布检查：

```powershell
.\scripts\test-msi-command.ps1
.\scripts\test-msi-package.ps1
.\scripts\test-msi-release.ps1
```

存储规模测试默认关闭。独立执行时可以临时启用：

```powershell
$env:DGP_STORAGE_SCALE_TEST = '1'
go -C backend test ./internal/storage -run TestStorageScale -count=1
Remove-Item Env:DGP_STORAGE_SCALE_TEST
```

`2.11.44` 的 Go 包回归、vet、钩子/代理/服务相关 race、原生状态和界面结构、
Release 构建及设置、自启动、偏好持久化和启动等待回归通过。键鼠回归使用模拟回调，未启用真实钩子。
发布包经过 MSI 门禁、WiX/ICE、四个组件与 MSI 签名、产品版本和构建回执
SHA-256 核对。目标设备安装升级及真实键鼠锁定、远程输入仍待实机验收。

## 已知限制

以下限制会影响采集覆盖范围、审计解释能力或正式交付验证。

- 进程快照默认每 5 分钟执行一次，周期内启动并结束的短生命周期进程可能不会进入
  快照；筛选规则也会主动忽略常规系统组件。
- 文件进程归因依赖 Windows 审核记录或可关联事件。无法归因时会保留缺失状态。
- 内容哈希会跳过目录、链接和非常规文件，较大文件进入后台限速队列。
- USN 补偿只适用于 NTFS 卷。无法解析完整路径时会生成采集健康事件。
- 严格读取审计需要管理员权限和可访问的 Windows 安全日志。
- Session Agent 依赖目标用户会话中的代理进程。代理不可用时会产生采集缺口。
- 临时输入控制依赖交互式代理和可用的解锁凭据。服务暂时不可连接时，正在运行的
  代理会保留当前钩子，直到连接恢复并读取到任务停止或到期状态。
- Windows 10 1809、Windows 11、睡眠恢复、磁盘满、设备拔除和安全日志清理仍需
  在目标设备执行发布门槛验证。
- MSI 仅接受指定旧测试证书到当前 Raymond 证书的迁移，其他发布者变化仍拒绝。

## 第三方许可

项目使用的第三方组件、版本、版权和许可正文记录在
[`THIRD-PARTY-NOTICES.txt`](THIRD-PARTY-NOTICES.txt)。发布时必须随程序保留该文件。
