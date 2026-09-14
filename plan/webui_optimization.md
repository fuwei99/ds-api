DS2API WebUI 重构详细方案
已确认决策：渐进式 TS 迁移 / 少量引入 shadcn 风格基础组件 / zustand / ESLint+Prettier / 分 4 个 PR
目标目录结构（终态）
webui/
├── src/
│   ├── main.tsx
│   ├── app/                        # 应用装配层
│   │   ├── App.tsx                 # providers + router（替代现 App.jsx/AppRoutes.jsx）
│   │   ├── routes.tsx              # 路由表（嵌套路由 + lazy + guard）
│   │   └── providers.tsx
│   ├── lib/                        # 基础设施（全部 TS）
│   │   ├── api/
│   │   │   ├── client.ts           # 统一请求客户端
│   │   │   ├── errors.ts           # ApiError 错误归一化
│   │   │   ├── adminApi.ts         # 全部 /admin/* 端点函数
│   │   │   └── v1Api.ts            # /v1/* 业务端点（SSE 在 PR3 抽入）
│   │   ├── hooks/usePolling.ts     # 统一轮询治理
│   │   ├── utils/{cn.ts, runtimeEnv.ts, maskSecret.ts, batchImportTemplates.ts}
│   │   └── storage.ts              # localStorage key 集中管理
│   ├── stores/                     # zustand
│   │   ├── authStore.ts            # token/登录/登出/verify
│   │   ├── configStore.ts          # admin config
│   │   └── toastStore.ts           # 全局消息提示
│   ├── i18n/{index.tsx, locales/}  # 由 i18n.jsx + locales/ 迁入（逻辑不变）
│   ├── layouts/
│   │   ├── DashboardLayout.tsx     # 布局 + <Outlet/>
│   │   └── Sidebar.tsx
│   ├── components/
│   │   ├── ui/                     # shadcn 风格基础件：button/dialog/input/badge
│   │   └── LanguageToggle.tsx
│   ├── pages/{LandingPage.tsx, LoginPage.tsx}
│   └── features/                   # 保持现有 7 域划分，内部统一 "Container + 子组件 + hooks + api" 分层
│       ├── account/  apiTester/  chatHistory/  proxy/  settings/  vercel/  import/
└── tests/ 或 src/**/*.test.tsx     # vitest


PR 1 · 工具链 + 统一请求层 + 刷新修复
目标：把规范工具全部立起来并接入 CI；建统一请求层并迁移全部 /admin/* 调用；根治 dev 刷新异常。
1.1 工具链
- Prettier：.prettierrc（semi: false, singleQuote: true, tabWidth: 4, printWidth: 100——贴合现有风格，最小化 diff）+ prettier-plugin-tailwindcss（自动排序 class，杜绝后续样式性 diff——正是反馈担心的"样式问题大范围改动"）
- 一次性全量格式化作为独立 commit（纯机械变更），可选加 .git-blame-ignore-revs
- ESLint 9 flat config：@eslint/js + typescript-eslint + eslint-plugin-react-hooks（rules-of-hooks=error，exhaustive-deps=warn 起步）+ eslint-plugin-react-refresh + eslint-config-prettier；修掉全部 error；删除两处摆设性 // eslint-disable 注释
- TypeScript：tsconfig.json（strict: true, allowJs: true, checkJs: false, jsx: react-jsx, moduleResolution: bundler）——tsx 文件严格检查，jsx 暂不强检，实现渐进迁移；npm run typecheck = tsc --noEmit
- vitest：vitest + jsdom + @testing-library/react + jest-dom，vitest.config.ts 与 vite 配置分离
- npm scripts：lint / format / format:check / typecheck / test
- 此 PR 新建文件（lib/api、lib/hooks、lib/utils、lib/storage）直接写 TS
1.2 统一请求层（核心）
// lib/api/client.ts 设计
configureApiClient({ getToken, onUnauthorized })   // 依赖注册，避免与 store 循环引用
apiFetch(path, { method, body, query, timeout = 30s, auth = true })
// 统一：token 注入 / 401→登出回调+抛错 / AbortController 超时
//       非 JSON 响应容错解析（合并现有两份重复实现）/ ApiError 归一化（提取后端错误消息）
- adminApi.ts：把现有 ~30 个 /admin/* 端点全部收敛为命名函数（参照现有 settingsApi.js 模式推广）
- 迁移全部调用点：各组件改为直接 import adminApi，删除 DashboardShell 里的 authFetch 及其 props 下传，消灭 3 处 authFetch || fetch 裸 fetch fallback（Login/verify/config 三处也改用 client，auth: false 选项支持 login）
- usePolling.ts：统一 interval/退避/防重入，本期先迁移最简单的 useAccountsData（5s），ETag 轮询和 vercel 退避留到 PR3 对应 feature 重构时迁移
- storage.ts：集中 ds2api_token 等全部 storage key
1.3 刷新异常修复（dev）
vite.config.js 的 bypass 改为按请求头判断页面请求：
bypass(req) {
  if (req.method === 'GET' && (req.headers.accept || '').includes('text/html')) return '/index.html'
}
根治"刷新 /admin/accounts 被代理到后端返回旧 build/404"的问题（带查询串、任意 tab 路径都覆盖）。
1.4 CI 与门禁
- quality-gates.yml：webui-build job 扩展为 npm run lint && format:check && typecheck && test && build
- 更新根 AGENTS.md PR Gate 章节，加入 webui 本地门禁命令（Documentation Sync 要求）
- 顺手验证并移除死依赖 uuid（tailwind-merge 保留，PR3 的 cn() 会启用它）
验收：npm run lint/format:check/typecheck/test/build 全绿；admin 全部功能手工冒烟通过；dev 下刷新任意 tab 正常。


PR 2 · zustand 状态管理 + 真路由 + 目录重排
目标：消灭 props drilling；tab 真路由化；完成目录迁移（git mv 保留历史）。
2.1 三个 store
- authStore：token / status('checking'|'authed'|'guest') / isVercel，actions：initialize()（读 storage + verify）/ login() / logout()；模块内完成 configureApiClient 注册
- configStore：config / fetchConfig()（调 adminApi）
- toastStore：message / show(type, text)（自动 5s 消失）
- 组件全部改为 useXStore() 自取，t 改用 useI18n() 直取，删除全部透传 props
2.2 路由重构
/            → LandingPage（仅 dev，保持现状）
/login       → LoginPage（独立路由，替代三元表达式）
/ (RequireAuth → DashboardLayout)
   ├── index → <Navigate to="accounts">
   ├── accounts / proxies / test / history / import / vercel / settings  (均 lazy)
   └── *     → <Navigate to="accounts">
- <RequireAuth> guard：checking→spinner；无 token→<Navigate to="/login" state={{from}}>；登录后回跳 from
- 删除 DashboardShell 手写 path 解析 + switch；侧边栏改 NavLink；DashboardLayout 拆出 Sidebar
- 保持现有双模式行为（dev basename / + /admin/*，prod basename /admin）与现有 URL 兼容，tab 路径不变
- 目录重排（git mv）：App.jsx 2 行转发层删除、BatchImport→features/import/、Login/LandingPage→pages/、i18n.jsx→i18n/、入口 main.tsx
行为变更（需你知晓）：/admin/verify 网络失败目前 fail-open 放行，建议改为失败提示+重试（fail-closed），属安全收紧——如不接受可保持现状，我会显式标注。
验收：所有 tab URL 直达/刷新/回退正常；未登录访问任意 admin 路径跳转登录页；登出后回登录页；全量手工冒烟。


PR 3 · 组件拆分 + UI 基础组件
目标：消灭 400+ 行文件与 30+ props 组件；引入 shadcn 风格基础件。以 settings 目录现有 "Container + Section + api" 模式为模板。
对象	拆分方案
useAccountActions.js (540)	按域拆 hooks/useAccountsApi.ts、useKeysApi.ts、useElasticPool.ts + modal 状态就近放置
AccountsTable.jsx (343, 32 props)	拆 AccountToolbar / AccountRow / Pagination，props 收敛到个位数
ProxyManagerContainer.jsx (464)	拆 ProxiesTable / ProxyModal / useProxiesApi.ts（4 个内联组件独立成文件）
ChatHistoryContainer.jsx (433)	ETag 轮询抽 useChatHistorySync.ts（迁到 usePolling），详情面板沿用现有拆分
useChatStreamClient.js (282, 16 参)	改 options 对象传参；SSE 解析抽 lib/api/sse.ts
ChatHistoryPanels/Detail	维持现状为主，仅随 TS 转换整理
UI 基础件（components/ui/，手写 shadcn 风格，不用 CLI）：
- 新增依赖：class-variance-authority + @radix-ui/react-dialog + tailwindcss-animate（顺带修好全项目失效的 animate-in 类）；cn() = clsx + tailwind-merge（激活死依赖）
- Button（替代 .btn-* 组件类与手写按钮两套并存）、Dialog（Radix，替换 7 处手写 modal 外壳，获得焦点圈定/ESC/ARIA）、Input、Badge
- 样式修复：btn-primary hover 颜色 bug、收敛 styles.css 冗余组件类
- 各 feature 文件顺手完成 .jsx→.tsx 转换
验收：全部文件 < 400 行；组件 props ≤ 10 个左右；功能零回归冒烟。


PR 4 · 测试 + 剩余 TS 转换 + 收尾
- 测试：client.ts（401/超时/错误归一化）、storage.ts、authStore、usePolling、纯函数（maskSecret/batchImportTemplates/chatHistoryUtils）、组件冒烟（Login 提交、RequireAuth 重定向、Sidebar 渲染）；CI test 步骤此时已有实质覆盖
- 剩余 .jsx→.tsx 转换（依赖推断 + 少量注解），完成后 allowJs: false 收口
- 收尾清理：LandingPage 的 <style> 注入迁 Tailwind（dev-only，低优先）、exhaustive-deps 评估升 error
- 文档：检查 README/docs 中 webui 开发说明并同步；最终核对 AGENTS.md
风险与注意事项
1. PR2 目录大迁移：全程 git mv + 改完立即 build 验证 + 全局 grep 残留旧路径
2. 门禁兼容：check-refactor-line-gate.sh 的 targets 文件当前不存在（门禁空转），重构后所有文件均 <500 行无触线风险；但脚本里 is_entry_file 硬编码了 webui/src/App.jsx，若日后启用 targets 需同步该路径——会在 PR2 备注
3. 重构不改业务逻辑（AGENTS.md 要求）：exhaustive-deps 暴露的疑似 bug 只做最小修复并单独标注，不夹带逻辑变更
4. docs/prompt-compatibility.md 涉及的是 API 兼容流，本次不触及，无需更新
5. 生产环境 SPA fallback 后端已支持（无点路径回退 index.html），新路由无点段，无风险