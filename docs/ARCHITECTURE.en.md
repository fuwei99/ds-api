# DS2API Architecture & Project Layout

Language: [中文](ARCHITECTURE.md) | [English](ARCHITECTURE.en.md)

> This file is the single architecture source for directory layout, module boundaries, and execution flow.

## 1. Top-level Layout (core directories)

> Notes: this lists the main business directories (excluding metadata/dependency dirs such as `.git/` and `webui/node_modules/`), with each folder annotated by purpose. Newly added directories should be verified from the code tree rather than treated as a per-file inventory here.

```text
ds2api/
├── .github/                              # GitHub collaboration and CI config
│   ├── ISSUE_TEMPLATE/                   # Issue templates
│   └── workflows/                        # GitHub Actions workflows
├── api/                                  # Serverless entrypoints (Vercel Go/Node)
├── app/                                  # Application-level handler assembly
├── artifacts/                            # Debug artifacts (raw-stream-sim, stream-debug, etc.)
├── cmd/                                  # Executable entrypoints
│   ├── ds2api/                           # Main service bootstrap
│   └── ds2api-tests/                     # E2E testsuite CLI bootstrap
├── docs/                                 # Project documentation
├── internal/                             # Core implementation (non-public packages)
│   ├── account/                          # Account pool, inflight slots, waiting queue
│   ├── auth/                             # Auth/JWT/credential resolution
│   ├── chathistory/                      # Server-side conversation history storage/query
│   ├── claudeconv/                       # Claude message conversion helpers
│   ├── compat/                           # Compatibility and regression helpers
│   ├── assistantturn/                    # Upstream output to canonical assistant turn / stream event semantics
│   ├── completionruntime/                # Shared Go DeepSeek completion startup, collection, empty-output/account-switch retry
│   ├── config/                           # Config loading/validation/hot reload
│   ├── deepseek/                         # DeepSeek upstream client/protocol/transport
│   │   ├── client/                       # Login/session/completion/upload/delete calls
│   │   ├── protocol/                     # DeepSeek URLs, constants, skip path/pattern
│   │   └── transport/                    # DeepSeek transport details
│   ├── devcapture/                       # Dev capture and troubleshooting
│   ├── format/                           # Response formatting layer
│   │   ├── claude/                       # Claude output formatting
│   │   └── openai/                       # OpenAI output formatting
│   ├── httpapi/                          # HTTP surfaces: OpenAI/Claude/Gemini/Admin
│   │   ├── admin/                        # Admin API root assembly and resource packages
│   │   ├── claude/                       # Claude HTTP protocol adapter
│   │   ├── gemini/                       # Gemini HTTP protocol adapter
│   │   ├── ollama/                       # Ollama-compatible model/capability query endpoints
│   │   ├── openai/                       # OpenAI HTTP surface
│   │   │   ├── chat/                     # Chat Completions execution entrypoint
│   │   │   ├── responses/                # Responses API and response store
│   │   │   ├── files/                    # Files API and inline-file preprocessing
│   │   │   ├── embeddings/               # Embeddings API
│   │   │   ├── history/                  # OpenAI context file handling
│   │   │   └── shared/                   # OpenAI HTTP errors/models/tool formatting
│   │   └── requestbody/                  # HTTP body reading and UTF-8/JSON validation helpers
│   ├── js/                               # Node runtime related logic
│   │   ├── chat-stream/                  # Node streaming bridge
│   │   ├── helpers/                      # JS helper modules
│   │   │   └── stream-tool-sieve/        # JS implementation of tool sieve
│   │   └── shared/                       # Shared semantics between Go/Node
│   ├── prompt/                           # Prompt composition
│   ├── promptcompat/                     # API request -> DeepSeek web-chat plain-text compatibility
│   ├── rawsample/                        # Raw sample read/write and management
│   ├── responsehistory/                  # DeepSeek upstream response archive and session snapshots
│   ├── server/                           # Router and middleware assembly
│   │   └── data/                         # Router/runtime helper data
│   ├── sse/                              # SSE parsing utilities
│   ├── stream/                           # Unified stream consumption engine
│   ├── testsuite/                        # Testsuite execution framework
│   ├── textclean/                        # Text cleanup
│   ├── toolcall/                         # Tool-call parsing and repair
│   ├── toolstream/                       # Go streaming tool-call anti-leak and delta detection
│   ├── translatorcliproxy/               # Vercel/fallback/test protocol translation bridge
│   ├── util/                             # Shared utility helpers
│   ├── version/                          # Version query/compare
│   └── webui/                            # WebUI static hosting logic
├── plans/                                # Stage plans and manual QA records
├── pow/                                  # PoW standalone implementation + benchmarks
├── scripts/                              # Build/release helper scripts
├── static/                               # Build artifacts (admin static resources)
├── tests/                                # Test assets and scripts
│   ├── compat/                           # Compatibility fixtures + expected outputs
│   │   ├── expected/                     # Expected output samples
│   │   └── fixtures/                     # Fixture inputs
│   │       ├── sse_chunks/               # SSE chunk fixtures
│   │       └── toolcalls/                # Tool-call fixtures
│   ├── node/                             # Node unit tests
│   ├── raw_stream_samples/               # Upstream raw SSE samples
│   │   ├── continue-thinking-snapshot-replay-20260405/    # Continue-thinking sample
│   │   ├── longtext-deepseek-v4-flash-20260429/           # Flash long-text/file-upload sample
│   │   ├── longtext-deepseek-v4-pro-20260429/             # Pro long-text/file-upload sample
│   │   ├── markdown-format-example-20260405/              # Markdown sample
│   │   └── markdown-format-example-20260405-spacefix/     # Space-fix sample
│   ├── scripts/                          # Test entry scripts
│   └── tools/                            # Testing helper tools
└── webui/                                # React admin console source
    ├── public/                           # Static assets
    └── src/                              # Frontend source code
        ├── app/                          # Routing/state scaffolding
        ├── components/                   # Shared UI components
        ├── features/                     # Feature modules
        │   ├── account/                  # Account management page
        │   ├── apiTester/                # API tester page
        │   ├── chatHistory/              # Server-side conversation history page
        │   ├── proxy/                    # Proxy management page
        │   ├── settings/                 # Settings page
        │   └── vercel/                   # Vercel sync page
        ├── layout/                       # Layout components
        ├── locales/                      # i18n strings
        └── utils/                        # Frontend utilities
```

## 2. Primary Request Flow

```mermaid
flowchart LR
    C[Client / SDK] --> R[internal/server/router.go]

    subgraph HTTP[HTTP API surface]
        OA[internal/httpapi/openai]
        CHAT[openai/chat]
        RESP[openai/responses]
        FILES[openai/files + embeddings]
        CA[internal/httpapi/claude]
        GA[internal/httpapi/gemini]
        AD[internal/httpapi/admin/*]
        WEB[internal/webui static admin]
    end

    subgraph COMPAT[Prompt compatibility core]
        PC[internal/promptcompat]
        PROMPT[internal/prompt]
        HIST[internal/httpapi/openai/history]
    end

    subgraph RUNTIME[Shared runtime]
        AUTH[internal/auth]
        POOL[internal/account queue + concurrency]
        CR[internal/completionruntime]
        TURN[internal/assistantturn]
        STREAM[internal/stream + internal/sse]
        TOOL[internal/toolcall + internal/toolstream]
        FMT[internal/format/openai + claude]
        DS[internal/deepseek/client]
        POW[pow + internal/deepseek/protocol]
    end

    subgraph NODE[Vercel Node Runtime]
        NCS[api/chat-stream.js]
        JS[internal/js/chat-stream + stream-tool-sieve]
    end

    R --> OA --> CHAT
    OA --> RESP
    OA --> FILES
    R --> CA
    R --> GA
    R --> AD
    R --> WEB
    R -.Vercel stream.-> NCS

    CA --> PC
    GA --> PC
    CHAT --> PC
    RESP --> PC
    PC --> PROMPT
    PC -.long history.-> HIST
    PC --> AUTH
    PC --> CR

    NCS -.Go prepare/release.-> CHAT
    NCS --> JS
    JS --> TOOL

    AUTH --> POOL
    CHAT --> CR
    RESP --> CR
    CA --> CR
    GA --> CR
    CR --> DS
    CR --> STREAM
    CR --> TURN
    STREAM --> TURN
    STREAM --> TOOL
    TURN --> FMT
    POOL --> CR
    DS --> POW
    DS --> U[DeepSeek upstream]
```

## 3. Responsibilities in `internal/`

- `internal/server`: router tree + middlewares (health, protocol routes, Admin/WebUI).
- `internal/httpapi/openai/*`: OpenAI HTTP surface split into chat, responses, files, embeddings, history, and shared packages; chat/responses share the promptcompat, stream, and toolcall semantics.
- `internal/httpapi/{claude,gemini}`: protocol adapters that normalize into the same prompt compatibility semantics; normal direct paths must share DeepSeek session/PoW/completion execution through `completionruntime`, while `translatorcliproxy` is reserved for Vercel prepare/release, missing-backend fallback, and regression tests.
- `internal/httpapi/ollama`: Ollama-compatible model list and capability query endpoints.
- `internal/httpapi/requestbody`: shared HTTP body reading, JSON pre-validation, and UTF-8 error helpers across protocol adapters.
- `internal/promptcompat`: compatibility core for turning OpenAI/Claude/Gemini requests into DeepSeek web-chat plain-text context.
- `internal/assistantturn`: Go output-side canonical semantics, converting DeepSeek SSE collection results and stream finalization state into assistant turns and centralizing thinking, tool call, citation, usage, stop/error behavior.
- `internal/completionruntime`: shared Go completion execution helpers for DeepSeek session/PoW/call startup, non-stream collection, empty-output retry, and one managed-account fresh retry before a final 429; streaming paths use it to start upstream requests, continue to use `internal/stream` for real-time consumption, and use `assistantturn` during finalization.
- `internal/translatorcliproxy`: bridge compatibility layer for Claude/Gemini and OpenAI shape translation; it is not the main business protocol conversion center.
- `internal/deepseek/{client,protocol,transport}`: upstream requests, sessions, PoW adaptation, protocol constants, and transport details. Both primary and fallback transports use httpcloak's chrome-150-windows fingerprint (identical TLS ClientHello / H2 / header order); the fallback only switches protocol negotiation from forced H2 to auto (H1 when H2 fails) and never degrades to the bare Go standard library, whose crypto/tls handshake is a bot tell.
- `internal/js/chat-stream` + `api/chat-stream.js`: Vercel Node streaming bridge; Go prepare/release owns auth, account lease, and completion payload assembly, while Node relays real-time SSE with Go-aligned finalization and tool sieve semantics.
- `internal/stream` + `internal/sse`: Go stream parsing and incremental assembly.
- `internal/toolcall` + `internal/toolstream`: EPSE shell compatibility plus canonical XML tool-call parsing and anti-leak sieve; EPSE is normalized back to XML at the entrypoint, and internal parsing remains XML-based.
- `internal/httpapi/admin/*`: Admin API root assembly plus auth/accounts/config/settings/proxies/rawsamples/vercel/history/devcapture/version resource packages.
- `internal/chathistory`: server-side conversation history persistence, pagination, detail lookup, and retention policy.
- `internal/responsehistory`: DeepSeek upstream response archive, saving assistant text, thinking, raw tool-call fragments, and streaming detail before protocol rendering/trimming.
- `internal/usagestats`: Token usage ledger aggregating counts and cost estimates by GMT+8 date + model + caller; cost is estimated once at record time using the then-current prices (peak windows resolved in GMT+8, multiplier applied) and never retroactively recomputed; a price of 0 means free. Off-window dates are waived by the "exclude weekends & public holidays" setting (JSON key kept as the historical `weekend_normal`): when enabled, weekends and statutory holidays are billed at the idle price for the whole day, with holiday data from `github.com/wangzeping722/chinesecalendar` (covering 2004-01-01 through 2026-10-10); outside that range it degrades to "weekends only". That library indexes dates by `time.Local`, so the GMT+8 year/month/day must be re-materialized in `time.Local` before lookup. After the model merge every public model (`deepseek-v4.1-flash` and its `-search` / `-nothinking` variants) shares one price, so the price table holds only the single `deepseek-v4.1-flash` entry (variants match it by `-` segments) and the settings page renders one input/output pair. Loading settings collapses the pre-merge tier keys (`flash` / `vision` / `pro` / `v4-pro`) and the former standalone `deepseek-v4.1-flash-search` key into that entry; all other keys are dropped.
- `internal/usagestats` "simulate cache hit" (`cache_hit`): upstream never reports cache hits, so the configured hit rate is simulated — input tokens are split into a hit part (`input_price`, shown as "Input (cache hit)" in the settings page) and a miss part (`input_miss_price`, default 1 CNY per 1M tokens), billed separately before the output cost and peak multiplier are applied. When disabled, the whole input is billed at `input_price`, exactly as before. It defaults to enabled with a 98% hit rate, and the per-model "Token usage" chart in tokens mode splits "Input Tokens" into "Input (cache hit)" and "Input (cache miss)" using the same hit rate as the backend `inputCost`. `cache_hit.enabled`, `cache_hit.rate` and `input_miss_price` are decoded as pointers so that a missing key in an older settings file falls back to the default while an explicit `false` / `0` is preserved; the hit rate is clamped to `[0, 100]`.
- `internal/config`: config loading/validation + runtime settings hot-reload.
- `internal/account`: managed account pool, inflight slots, waiting queue.
- `internal/textclean`: text cleanup helpers, e.g. stripping `[reference: N]` markers.
- `internal/claudeconv`: Claude API request to DeepSeek format conversion.
- `internal/compat`: compatibility regression tests using SSE fixtures to verify output consistency.
- `internal/rawsample`: upstream raw response capture, read/write, and management.
- `internal/devcapture`: developer debug capture, storing HTTP request/response for troubleshooting.
- `internal/util`: cross-package utilities including JSON writing, type conversion, token counting, thinking parsing, etc.
- `internal/version`: version query and comparison, supporting build-time injection and runtime resolution.

## 4. WebUI Runtime Relation

- `webui/` stores frontend source (Vite + React).
- Runtime serves static output from `static/admin`.
- On first local startup, if `static/admin` is missing, DS2API may auto-build it (Node.js required).

## 5. Documentation Split Strategy

- Onboarding & quick start: `README.MD` / `README.en.md`
- Architecture & layout: `docs/ARCHITECTURE*.md` (this file)
- API contracts: `API.md` / `API.en.md`
- Deployment/testing/contributing: `docs/DEPLOY*`, `docs/TESTING.md`, `docs/CONTRIBUTING*`
- Deep topics: `docs/toolcall-semantics.md`, `docs/DeepSeekSSE行为结构说明-2026-04-05.md`
