# Desktop Guard Pro 最新验证记录

本记录核对 2026 年 9 月 30 日发布的 2.11.40。修复范围为 MSI 证书身份迁移。
源码、标签和五份附件已发布到 GitHub，2.11.38 与 2.11.39 发布物未覆盖。

## 失败证据

安装日志位于 `%ProgramData%\DesktopGuardPro\msi-maintenance-error.json`。
2.11.39 的 prepare 阶段记录于 `2026-09-27T14:02:24Z`，错误为
`verify MSI maintenance binary`，证书链终止于未受信任的根证书。

Raymond 证书当时存在于 CurrentUser 信任库，LocalMachine 信任库中缺失。
已将相同公开证书加入机器 Root 与 TrustedPublisher 库，私钥保持在原用户库。
实际 SYSTEM 账户的签名检查通过；临时检查任务执行后注销。
此轮未修改审计数据库、密钥、策略或安装清单，也未自动安装 MSI。

旧版本卸载后仍保留 2.11.38 安装清单，签名身份为旧测试证书。
现有 MSI 事务因此拒绝新的 Raymond 签名。新增测试先复现直接升级和保留数据
再安装两种拒绝，再修改事务判断使限定迁移通过。

## 迁移规则与实现选择

身份依据为证书 SHA-256，允许的迁移方向固定。

- 旧证书：`464D3524D6FBC620538DA0806FC260710A08D0AB77A854B111CB033CD91A4230`。
- Raymond 证书：`670117E1BD2C3DC9A61724A3176E88CB0D6B608E37360E094881770C0813EF71`。

旧证书摘要同时与安装清单和本机原代码签名证书核对，Raymond 摘要与公开证书核对。
只在 MSI prepare 阶段、所有者验证后允许该单向变化；后续事务保持新签名身份，
原安装清单和策略保存在回滚快照中。独立 `upgrade` 命令的规则保持不变。

| 实现方式 | 取舍 |
| --- | --- |
| 固定证书摘要对 | 本次采用，限定已确认的旧测试证书到 Raymond 证书，不新增配置。 |
| 通用证书轮换命令 | 需要额外的授权和轮换凭据管理，超出本次两个代码文件的修复范围。 |

代码位于 [MSI 事务](../../backend/internal/maintenance/msi_transaction_windows.go)，
回归位于 [MSI 事务测试](../../backend/internal/maintenance/msi_transaction_windows_test.go)。

## 自动化结果

以下检查均已通过。

| 检查 | 结果 |
| --- | --- |
| 迁移失败复现 | 直接升级和保留数据再安装均先返回 ErrUpgradePublisherMismatch。 |
| 允许迁移与回滚 | 两种安装状态通过；失败回滚保留旧签名、版本和策略快照。 |
| 拒绝不安全迁移 | 未知旧证书、同名不同证书、反向迁移、错误所有者和活动会话均拒绝，且不停止服务或创建事务。 |
| 事务内身份一致性 | apply、commit、rollback 阶段更换签名者均拒绝。 |
| `go -C backend test ./... -count=1 -timeout=120s` | 全量通过。 |
| `go -C backend vet ./...` | 无告警。 |
| `go -C backend test -race ./internal/maintenance -count=1 -timeout=120s` | 通过。 |
| 原生状态与界面结构 | Native.Tests 和 test-native-ui-shell.ps1 通过。 |
| MSI 签名门禁及顺序 | test-msi-release.ps1 通过。 |
| WiX/ICE 与安装表结构 | test-msi-package.ps1 通过，未安装夹具 MSI。 |
| 实际 2.11.40 构建 | 三个 Go 组件、WinUI 原生界面、四组件签名、清单定稿和 MSI 打包通过。 |
| 实际 MSI 只读检查 | ProductVersion 为 2.11.40，UI 版本为 2.11.40.0，四组件、清单和许可齐全。 |
| 实际 MSI 签名与回执 | Raymond 签名和 DigiCert 时间戳有效，回执版本、证书指纹与 MSI 哈希一致。 |
| 实际 SYSTEM 签名检查 | 账户 S-1-5-18，2.11.40 MSI 的签名状态为 Valid。 |

此前 2.11.39 的钩子 callback 指针检查和钩子/代理/服务 race 记录保存在 Git 历史。
本轮未修改这些模块；全量 Go 回归仍覆盖原有模拟 callback 用例，不启用真实钩子。

## 发布文件

文件位于 `releases/2.11.40/`，包括 MSI、回执、对应发布源码、公开证书和校验清单。
源码 ZIP 从 `v2.11.40` 标签生成，与发布提交一致。

MSI SHA-256：
`40700FA8823C493CDC4C798BCCC0F872F53B02C7E22D7915D2DF69AEA946B9A6`。
签名者为 `CN=Raymond`，证书指纹为
`DC38087948CDD021FD794F0D7B6B4F5A12345AEE`。

证书有效期为 2026 年 9 月 27 日至 2029 年 9 月 27 日。
本机已经补齐系统账户的验证条件。旧原始发布包和历史源码 ZIP 保持不变。

## 实机验收范围

新 MSI 的实际安装结果仍需按用户反馈及安装日志核对。
自动化检查不代替真实安装、七页原生操作、物理键鼠、远程输入及 Windows 10/11
兼容性验收。完整功能条件见 [需求验收矩阵](../requirements-acceptance-matrix.md)。
