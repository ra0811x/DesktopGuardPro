# Desktop Guard Pro 项目协作说明

本文件提供进入项目时的上下文入口。项目使用 Windows 本机多进程架构，
界面、后台、安装维护和发布资料各自独立存放。

## 阅读入口

按任务范围读取项目说明，再定位直接相关的源码。

1. 读取 [PROJECT_CONTEXT.md](PROJECT_CONTEXT.md)，了解架构、目录和命令。
2. 读取 [PROJECT_STATUS.md](PROJECT_STATUS.md)，了解源码与发布包的差别。
3. 后台任务读取 [后端模块说明](docs/context/backend.md)；界面任务读取
   [前端模块说明](docs/context/frontend.md)。
4. 操作流程和功能边界见 [技术架构与操作说明](docs/technical-architecture.md)。

## 修改与验证

目录和协议属于构建、安装与运行时契约，修改时同步核对调用方和测试。

- Go 模块根目录为 `backend/`，模块名保持 `desktopguardpro`。
- `frontend/` 只保留 WinUI 3 原生界面与原生状态测试。
- `backend/internal/appbridge/` 和 `backend/internal/desktop/` 仍被维护程序调用。
- 保留用户未提交修改；不要执行会丢失工作区内容的 Git 命令。
- `releases/2.11.38/` 是已发布文件，不能覆盖已有 MSI 或混入未签名的测试输出。
- 新构建和临时检查输出放在 `dist/`，交付检查完成后清理。
- 对代码改动运行直接相关测试；涉及启动、发布路径或共享模块时运行全量检查。
- 键鼠控制回归使用模拟回调，不自动启用真实钩子或安装 MSI。

## 文档维护

文档随代码的持续变化更新，历史细节交由 Git 保存。

- 使用 `docs-writer` 技能处理项目文档。
- 更新 `PROJECT_STATUS.md` 中的有效状态与验证结果。
- 架构和目录职责改变时更新 `PROJECT_CONTEXT.md` 与模块说明。
- 最新验证记录统一保存为 `docs/verification/current.md`。
- 源码引用、构建命令与发布链接必须对应实际目录。
