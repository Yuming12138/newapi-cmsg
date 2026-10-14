# CMSG 前端约定

本文补充根目录 [AGENTS.md](../../AGENTS.md)，适用于 `web/default/`。
开发与发布流程见 [DEVELOPMENT.md](../../DEVELOPMENT.md)；依赖和命令以
[package.json](package.json) 为准。

## 项目结构

React 19、TypeScript、Rsbuild、Base UI 和 Tailwind 构成当前前端。
请求与状态使用 Axios、TanStack Query、Zustand；表单使用 React Hook Form
和 Zod。

- `src/features/<feature>/`：功能模块及其 API、组件、Hooks、类型和测试。
- `src/components/`：共享组件；`src/components/ui/`：现有 UI 基础组件。
- `src/lib/`、`src/stores/`：共享工具、请求实例和客户端状态。
- `src/routes/`：TanStack 文件路由；`src/routeTree.gen.ts` 由工具生成。
- `src/i18n/config.ts`、`src/i18n/locales/`：语言配置与翻译。
- `rsbuild.config.ts`：开发代理、环境变量与构建配置。

沿用相邻代码和现有组件，按职责组织新逻辑。文件拆分、props 解构和
memoization 由实际可读性或性能需要决定。

## 国际化

- 页面文案使用 `useTranslation()` 和 `t()`；非 React 工具可按需调用
  i18next。品牌和模型标识保持原名。
- 常规翻译键使用英文原文，例如 `t('Request failed')`；既有专题采用
  `geminiQuotaDetails.*` 等层级键时，保持该专题的组织方式。
- 运行时 fallback 为英文，支持 en、zh、fr、ru、ja、vi。新增文案同步
  英文与中文；`bun run i18n:sync` 按叶子键最多的资源选择同步基准，用英文
  补齐缺失值。执行后审查资源与报告差异。
- 常量中的文案存翻译键，展示时调用 `t()`。动态键按现有提取方式登记到
  `src/i18n/static-keys.ts`，避免扫描遗漏；审查同步产生的文件差异。

## 数据、错误与表单

- 使用 `src/lib/api.ts` 的 `api` 实例及公共请求头。普通数据请求复用
  feature API 和 TanStack Query；SSE 等流式请求沿用专用实现。
- Query key 包含影响结果的参数；变更成功后定向刷新相关数据。Zustand
  使用所需字段的 selector，服务端数据留在 Query 缓存中。
- 错误提示复用 Axios 拦截器、Query 配置和 `handleServerError`，避免重复
  toast。可在原页面恢复的查询错误设置 `meta.errorHandledLocally`，保留面板
  与恢复入口；检查 `src/main.tsx` 的全局 500 重定向逻辑。
- 表单业务错误按字段显示。局部接管提示时，按既有代码使用
  `skipErrorHandler` 或 `skipBusinessError`，并提供对应的错误处理。
- 表单沿用 React Hook Form 与 Zod。编辑器保留输入中的未完成文本，在合适
  的保存或校验时机解析；稳定组件 key 和组件类型，防止逐键重挂载丢失焦点。
- 后端错误、加载、成功和取消均应有明确状态。长任务结果面板保留到用户
  主动关闭，关闭后停止无关轮询。

## 组件与安全

- 使用项目现有类型，类型专用导入用 `import type`；外部数据校验后使用。
- 沿用 `cn()`、主题变量、暗色模式和响应式布局。输入框有 label，弹窗、
  菜单和按钮可由键盘操作，关闭弹窗后恢复焦点。
- 持久化和重试逻辑遵循对应功能的既有策略；重试次数有上限，取消请求停止
  后续工作。memoization 或新增依赖应解决具体问题。
- 外部 HTML/SVG 经现有 sanitizer 后展示；保留 SVG 预览的隔离边界。模型
  输出按不可信输入处理，密钥和后台凭据保持在服务端。
- 保留 QuantumNous/new-api 署名、版权头和前端 attribution；生产构建保留
  第三方 license 文件。

## 命令与验收

在 `web/default/` 中运行：

~~~text
bun run dev
bun run typecheck
bun run build:check
bun run test:model-mapping
bun test <相关测试文件>
bun run lint
bun run format:check
bun run i18n:sync
~~~

当前回归测试使用 Bun 运行，已有测试采用 `node:test`，DOM 测试使用
happy-dom 和 React Testing Library。新增测试沿用相关目录的方式。

- TS/TSX 变更执行类型检查和相关测试；前端发布执行 `bun run build:check`。
- 对变更文件检查格式与 lint。行为修复重点覆盖触发问题的交互，例如连续
  输入、异步完成、错误恢复与关闭，而非固定行数或统一覆盖率门槛。
- 纯 Markdown 修改检查格式、链接及 diff，无需编译前端。
- `build:check` 已包含类型检查，无需在它之前重复执行同一检查。

Rsbuild 明确通过 `loadEnv` 保留 `VITE_` 前缀；例如
`VITE_REACT_APP_SERVER_URL` 用于开发代理，`VITE_REACT_APP_VERSION` 用于版本。
这是当前配置约定，公开构建变量只放可公开的值。生产产物为 `dist/`，
New API 编译前先完成前端构建。
