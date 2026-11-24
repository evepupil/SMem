import { DATA } from "./data.js";
import { initEmblemCanvas } from "./home.js";
import {
  STAMP_PALETTE,
  TRAIL_COLORS,
  copyText,
  esc,
  layoutGlyphLines,
  makeRng,
  scanSecrets,
  svgBitmapText,
  svgPixelIcon
} from "./graphics.js";

// Route 2: Clients Adapter Matrix (#/clients)
export function renderClients(main, runtime) {
  document.title = "适配矩阵" + DATA.copy.siteTitleSuffix;
  let filterMode = "all";
  let selectedId = DATA.clients[0].id;

  main.innerHTML = `
    <section class="sub-sec" data-surface="sodium">
      <div class="grid">
        <p class="col-1-3 t-micro">(适配矩阵)</p>
        <div class="col-5-12">
          <h1 class="t-h1" tabindex="-1">8+ 主流 AI 编程工具，一套纯 Markdown 记忆。</h1>
          <p class="t-body" style="margin-top:16px;">以下配置路径与注入行为严格对应源码 <code class="t-mono">internal/adapters/setup.go</code>。首次修改外部配置前自动备份至 <code class="t-mono">~/.smem/backups/</code>。</p>
          <div style="margin-top:24px;display:flex;flex-wrap:wrap;gap:8px;" id="client-filters">
            <button type="button" class="chip-toggle t-small" data-cf="all" aria-pressed="true">(全部 8 款)</button>
            <button type="button" class="chip-toggle t-small" data-cf="hook" aria-pressed="false">(支持 Lifecycle Hook)</button>
            <button type="button" class="chip-toggle t-small" data-cf="import" aria-pressed="false">(支持原生记忆导入)</button>
          </div>
        </div>
        <div class="col-full" id="clients-grid-box" style="margin-top:32px;"></div>
        <div class="col-full" id="client-detail-box" style="margin-top:48px;"></div>
      </div>
    </section>

    <section class="sec" data-surface="paper">
      <div class="grid">
        <p class="col-1-3 t-micro">(配置明细表)</p>
        <div class="col-5-12">
          <h2 class="t-h2">完整注册路径与原生导入对照</h2>
        </div>
        <div class="col-full" style="margin-top:24px;">
          <table class="data-table responsive-table">
            <thead>
              <tr>
                <th class="t-micro">客户端</th>
                <th class="t-micro">--agents</th>
                <th class="t-micro">MCP 注册位置</th>
                <th class="t-micro">Hook 接入</th>
                <th class="t-micro">规则 / Skill 入口</th>
                <th class="t-micro">原生导入</th>
              </tr>
            </thead>
            <tbody>
              ${DATA.clients.map(cl => `
                <tr>
                  <td data-label="客户端" class="t-small fw-600">${esc(cl.name)}</td>
                  <td data-label="--agents" class="t-micro t-mono">${esc(cl.flag)}</td>
                  <td data-label="MCP 注册位置" class="t-micro t-mono">${esc(cl.mcpPath)}</td>
                  <td data-label="Hook 接入" class="t-micro">${esc(cl.hookPath)}</td>
                  <td data-label="规则 / Skill" class="t-micro t-mono">${esc(cl.rulePath)}</td>
                  <td data-label="原生导入" class="t-micro t-mono">${esc(cl.importCmd)}</td>
                </tr>
              `).join("")}
              <tr>
                <td data-label="客户端" class="t-small fw-600">通用客户端 (Antigravity / Aider 等)</td>
                <td data-label="--agents" class="t-micro t-mono">—</td>
                <td data-label="MCP 注册位置" class="t-micro t-mono">stdio: command="smem", args=["mcp"]</td>
                <td data-label="Hook 接入" class="t-micro">—</td>
                <td data-label="规则 / Skill" class="t-micro t-mono">--project-rules 生成 AGENTS.md + SKILL.md</td>
                <td data-label="原生导入" class="t-micro t-mono">smem import --from dir --path &lt;目录&gt;</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>
  `;

  const gridBox = document.getElementById("clients-grid-box");
  const detailBox = document.getElementById("client-detail-box");
  const filterBtns = Array.from(document.querySelectorAll("#client-filters [data-cf]"));

  function renderDetail() {
    const cl = DATA.clients.find(x => x.id === selectedId) || DATA.clients[0];
    const cmd = `smem setup --agents ${cl.flag} --dry-run`;
    detailBox.innerHTML = `
      <div class="venue-details">
        <div style="display:flex;flex-wrap:wrap;justify-content:space-between;align-items:baseline;gap:12px;">
          <div>
            <p class="t-micro">(当前选中客户端 · --agents ${esc(cl.flag)})</p>
            <h2 class="t-h2" style="margin-top:4px;">${esc(cl.name)} — ${esc(cl.mode)}</h2>
          </div>
          <div class="cmd-inline">
            <span>$ ${esc(cmd)}</span>
            <button type="button" class="cmd-copy-btn" id="cl-copy-btn">复制</button>
          </div>
        </div>
        <p class="t-body" style="margin-top:16px;">${esc(cl.highlight)}</p>
        <ul class="access-list t-small" style="margin-top:20px;">
          <li><strong>MCP 注册位置：</strong><code class="t-mono">${esc(cl.mcpPath)}</code></li>
          <li><strong>Hook 接入位置：</strong>${esc(cl.hookPath)}</li>
          <li><strong>规则 / Skill：</strong><code class="t-mono">${esc(cl.rulePath)}</code></li>
          <li><strong>历史记忆迁入：</strong><code class="t-mono">${esc(cl.importCmd)}</code>（${esc(cl.importNote)}）</li>
        </ul>
      </div>
    `;
    document.getElementById("cl-copy-btn").addEventListener("click", e => copyText(cmd, e.currentTarget));
  }

  function renderGrid() {
    const list = DATA.clients.filter(cl => {
      if (filterMode === "hook") return cl.hasHook;
      if (filterMode === "import") return cl.hasImport;
      return true;
    });
    if (!list.some(x => x.id === selectedId) && list[0]) selectedId = list[0].id;
    gridBox.innerHTML = `<div class="speakers-preview-grid">
      ${list.map(cl => `
        <figure class="speaker-card">
          <button type="button" class="speaker-card-link" style="width:100%;text-align:left;${cl.id === selectedId ? "outline:3px solid var(--ink);outline-offset:4px;" : ""}" data-select-cl="${esc(cl.id)}">
            <canvas class="portrait-canvas" data-clid="${esc(cl.id)}" role="img" aria-label="${esc(cl.name)}"></canvas>
            <figcaption class="speaker-card-figcap">
              <h3 class="t-lead speaker-card-name">${esc(cl.name)}</h3>
              <p class="t-micro speaker-card-role">${esc(cl.mode)}</p>
              <p class="t-micro t-mono speaker-card-talk">${cl.hasHook ? "MCP + Hook" : "MCP + Rules"}</p>
            </figcaption>
          </button>
        </figure>
      `).join("")}
    </div>`;

    list.forEach(cl => {
      const cv = gridBox.querySelector(`canvas[data-clid="${cl.id}"]`);
      if (cv) initEmblemCanvas(cv, cl, runtime);
    });

    gridBox.querySelectorAll("[data-select-cl]").forEach(btn => {
      btn.addEventListener("click", () => {
        selectedId = btn.dataset.selectCl;
        renderGrid();
        renderDetail();
      });
    });
  }

  filterBtns.forEach(btn => {
    btn.addEventListener("click", () => {
      filterMode = btn.dataset.cf;
      filterBtns.forEach(b => b.setAttribute("aria-pressed", b.dataset.cf === filterMode ? "true" : "false"));
      renderGrid();
      renderDetail();
    });
  });

  renderGrid();
  renderDetail();
}

// Route 3: Architecture & Security (#/architecture)
export function renderArchitecture(main) {
  document.title = "架构与安全" + DATA.copy.siteTitleSuffix;
  let selectedId = DATA.archModules[0].id;

  main.innerHTML = `
    <section class="sub-sec" data-surface="paper">
      <div class="grid">
        <p class="col-1-3 t-micro">(架构蓝图)</p>
        <div class="col-5-12">
          <h1 class="t-h1" tabindex="-1">单一静态 Go 二进制，零魔法分层。</h1>
          <p class="t-body" style="margin-top:16px;">点击架构图中的任意模块或右侧索引，查看 SMem 的内部包职责、跨平台锁机制与纯 Markdown 双层存储布局。</p>
        </div>
      </div>

      <div class="grid venue-grid">
        <div class="col-1-8">
          <svg class="venue-svg" id="arch-svg" viewBox="0 0 40 24" shape-rendering="crispEdges" role="img" aria-label="SMem 模块架构图"></svg>
          <p class="t-micro" style="margin-top:8px;">(Fig. 2 — SMem 模块分层图：三通道接入 → 单二进制内核 → 纯 Markdown 双层存储。)</p>
        </div>
        <div class="col-9-12">
          <div class="venue-room-list" id="arch-list">
            ${DATA.archModules.map(m => `
              <button type="button" class="venue-room-btn t-small" data-mod="${esc(m.id)}" aria-pressed="${m.id === selectedId ? "true" : "false"}">
                <span class="fw-600">${esc(m.label)}</span>
                <span class="t-micro t-mono">${esc(m.chip)}</span>
              </button>
            `).join("")}
          </div>
          <div class="venue-details" id="arch-details"></div>
        </div>
      </div>
    </section>

    <section class="sec" data-surface="ink">
      <div class="grid">
        <p class="col-1-3 t-micro">(11 类确定性密钥守卫)</p>
        <div class="col-5-12">
          <h2 class="t-h2 t-paper">拒绝凭证落盘与外泄。MCP 模式下不可绕过。</h2>
          <p class="t-body t-paper" style="margin-top:16px;opacity:0.86;">写入与 <code class="t-mono">smem share push</code> 前强制经过 <code class="t-mono">internal/guard</code> 确定性正则与结构扫描；自动剥离 <code class="t-mono">&lt;private&gt;...&lt;/private&gt;</code> 并逐级 <code class="t-mono">Lstat</code> 拒绝符号链接目录写入。</p>
        </div>
        <div class="col-full" style="margin-top:32px;">
          <table class="data-table responsive-table">
            <thead>
              <tr>
                <th class="t-micro">编号</th>
                <th class="t-micro">拦截凭证类别</th>
                <th class="t-micro">典型匹配特征</th>
                <th class="t-micro">覆盖范围</th>
              </tr>
            </thead>
            <tbody>
              ${DATA.secretGuards.map(g => `
                <tr>
                  <td data-label="编号" class="t-micro t-mono">${esc(g.id.toUpperCase())}</td>
                  <td data-label="拦截类别" class="t-small fw-600 t-paper">${esc(g.name)}</td>
                  <td data-label="典型特征" class="t-micro t-mono t-sodium">${esc(g.sample)}</td>
                  <td data-label="覆盖范围" class="t-small t-paper">${esc(g.scope)}</td>
                </tr>
              `).join("")}
            </tbody>
          </table>
        </div>
      </div>
    </section>

    <section class="sec" data-surface="sodium">
      <div class="grid">
        <p class="col-1-3 t-micro">(目录与格式)</p>
        <div class="col-5-12">
          <h2 class="t-h2">扁平目录，Claude Code 原生兼容。</h2>
          <div class="code-header" style="margin-top:24px;">
            <span class="t-micro fw-600">作用域目录结构 (~/.smem/projects/&lt;repo-id&gt;/)</span>
            <span class="t-micro t-mono">UTF-8 Markdown</span>
          </div>
          <pre class="code-block">&lt;memory-dir&gt;/
├── MEMORY.md                     # 自动维护的入口索引（硬控 ≤ 200 行 / 25KB）
├── user_backend_profile.md       # 个人背景与沟通偏好 (global)
├── feedback_terse_output.md      # 纠错与工作流纪律
├── project_release_owner.md      # 项目决策与约束
└── reference_ops_dashboard.md    # 外部看板 / 工单系统指针</pre>
        </div>
      </div>
    </section>
  `;

  const svg = document.getElementById("arch-svg");
  const detailsEl = document.getElementById("arch-details");
  const listBtns = Array.from(document.querySelectorAll("#arch-list [data-mod]"));

  function paint() {
    let out = `<rect x="0" y="0" width="40" height="24" fill="#FFFFFF" stroke="#0A0A0A" stroke-width="0.35"/>`;
    DATA.archModules.forEach(m => {
      const sel = m.id === selectedId;
      out += `<g class="venue-room-shape" data-mod="${esc(m.id)}" tabindex="0" role="button" aria-label="${esc(m.label)}">`;
      out += `<rect x="${m.x}" y="${m.y}" width="${m.w}" height="${m.h}" fill="${m.fill}" stroke="#0A0A0A" stroke-width="${sel ? "0.65" : "0.22"}"/>`;
      out += `<text x="${m.x + 0.7}" y="${m.y + 2.2}" font-family="Inter Tight, Noto Sans SC, sans-serif" font-size="1.4" font-weight="700" fill="${m.text}">${esc(m.label)}</text>`;
      out += `<text x="${m.x + 0.7}" y="${m.y + 4.0}" font-family="JetBrains Mono, monospace" font-size="0.95" fill="${m.text}" opacity="0.88">${esc(m.pkg)}</text>`;
      out += `<text x="${m.x + 0.7}" y="${m.y + m.h - 1.0}" font-family="JetBrains Mono, monospace" font-size="0.88" font-weight="600" fill="${m.text}">[${esc(m.chip)}]</text>`;
      out += `</g>`;
    });
    svg.innerHTML = out;

    svg.querySelectorAll("[data-mod]").forEach(el => {
      el.addEventListener("click", () => { selectedId = el.dataset.mod; paint(); });
      el.addEventListener("keydown", e => {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); selectedId = el.dataset.mod; paint(); }
      });
    });

    listBtns.forEach(b => b.setAttribute("aria-pressed", b.dataset.mod === selectedId ? "true" : "false"));

    const m = DATA.archModules.find(x => x.id === selectedId) || DATA.archModules[0];
    detailsEl.innerHTML = `
      <p class="t-micro t-mono">${esc(m.pkg)} · ${esc(m.chip)}</p>
      <h2 class="t-lead" style="margin-top:6px;">${esc(m.label)}</h2>
      <p class="t-small" style="margin-top:10px;">${esc(m.summary)}</p>
      <ul class="access-list t-small">
        ${m.details.map(d => `<li>${esc(d)}</li>`).join("")}
      </ul>
    `;
  }

  listBtns.forEach(b => b.addEventListener("click", () => { selectedId = b.dataset.mod; paint(); }));
  paint();
}

// Route 4: CLI & MCP Reference (#/docs)
export function renderDocs(main) {
  document.title = "CLI & MCP 手册" + DATA.copy.siteTitleSuffix;
  let query = "";
  let kindFilter = "all";
  let openId = "mcp-write";

  main.innerHTML = `
    <section class="sub-sec" data-surface="paper">
      <div class="grid">
        <p class="col-1-3 t-micro">(CLI & MCP 手册)</p>
        <div class="col-5-12">
          <h1 class="t-h1" tabindex="-1">5 个 MCP 工具，全套确定性 CLI。</h1>
          <p class="t-body" style="margin-top:16px;">所有读写与检索类 CLI 均支持 <code class="t-mono">--json</code> 机器可读输出；MCP Stdio 兼容 <code class="t-mono">2024-11-05</code> / <code class="t-mono">2025-03-26</code> / <code class="t-mono">2025-06-18</code>。点击任意一行展开调用示例。</p>
        </div>

        <div class="col-full" style="margin-top:32px;display:flex;flex-wrap:wrap;gap:16px;align-items:flex-end;">
          <div style="min-width:240px;flex:1;max-width:360px;">
            <label class="t-micro" for="docs-search">检索工具或命令</label>
            <input class="text-input" style="margin-top:6px;" type="search" id="docs-search" placeholder="如：etag、dream、share、memory_write">
          </div>
          <div style="display:flex;flex-wrap:wrap;gap:8px;" id="docs-kind-filters">
            <button type="button" class="chip-toggle t-small" data-kf="all" aria-pressed="true">(全部)</button>
            <button type="button" class="chip-toggle t-small" data-kf="MCP Tool" aria-pressed="false">(MCP Tools)</button>
            <button type="button" class="chip-toggle t-small" data-kf="CLI" aria-pressed="false">(CLI 命令)</button>
          </div>
        </div>

        <p class="col-full t-micro" id="docs-count" style="margin-top:16px;"></p>
        <div class="col-full" id="docs-table-wrap"></div>
      </div>
    </section>
  `;

  const searchEl = document.getElementById("docs-search");
  const kindBtns = Array.from(document.querySelectorAll("#docs-kind-filters [data-kf]"));
  const countEl = document.getElementById("docs-count");
  const tableWrap = document.getElementById("docs-table-wrap");

  function drawTable() {
    const q = query.trim().toLowerCase();
    const list = DATA.docsItems.filter(it => {
      if (kindFilter === "MCP Tool" && !it.kind.startsWith("MCP")) return false;
      if (kindFilter === "CLI" && it.kind !== "CLI") return false;
      if (q && !(it.name + " " + it.params + " " + it.summary + " " + it.example).toLowerCase().includes(q)) return false;
      return true;
    });
    countEl.textContent = `显示 ${list.length} / ${DATA.docsItems.length} 项接口`;

    if (list.length === 0) {
      tableWrap.innerHTML = `<p class="t-lead" style="margin-top:32px;">没有匹配的命令或 MCP 工具。</p>`;
      return;
    }

    tableWrap.innerHTML = `
      <table class="data-table responsive-table">
        <thead>
          <tr>
            <th class="t-micro">类型</th>
            <th class="t-micro">接口 / 命令</th>
            <th class="t-micro">关键参数</th>
            <th class="t-micro">说明</th>
          </tr>
        </thead>
        <tbody>
          ${list.map(it => {
            const isOpen = openId === it.id;
            return `
              <tr class="talk-row" data-cat="${esc(it.cat)}" data-doc-id="${esc(it.id)}">
                <td data-label="类型" class="t-micro t-mono">${esc(it.kind)}</td>
                <td data-label="接口 / 命令" class="t-small t-mono fw-600">${esc(it.name)}</td>
                <td data-label="关键参数" class="t-micro t-mono">${esc(it.params)}</td>
                <td data-label="说明" class="t-small">${esc(it.summary)}</td>
              </tr>
              ${isOpen ? `
                <tr class="talk-expand-row">
                  <td colspan="4">
                    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;">
                      <span class="t-micro fw-600">调用示例 · ${esc(it.name)}</span>
                      <button type="button" class="cmd-copy-btn" data-copy-example="${esc(it.id)}">复制代码</button>
                    </div>
                    <pre class="code-block">${esc(it.example)}</pre>
                  </td>
                </tr>
              ` : ""}
            `;
          }).join("")}
        </tbody>
      </table>
    `;

    tableWrap.querySelectorAll("[data-doc-id]").forEach(row => {
      row.addEventListener("click", () => {
        openId = openId === row.dataset.docId ? null : row.dataset.docId;
        drawTable();
      });
    });
    tableWrap.querySelectorAll("[data-copy-example]").forEach(btn => {
      btn.addEventListener("click", e => {
        e.stopPropagation();
        const found = DATA.docsItems.find(x => x.id === btn.dataset.copyExample);
        if (found) copyText(found.example, btn);
      });
    });
  }

  searchEl.addEventListener("input", () => { query = searchEl.value; drawTable(); });
  kindBtns.forEach(btn => {
    btn.addEventListener("click", () => {
      kindFilter = btn.dataset.kf;
      kindBtns.forEach(b => b.setAttribute("aria-pressed", b.dataset.kf === kindFilter ? "true" : "false"));
      drawTable();
    });
  });
  drawTable();
}

// Route 5: Quick Start & Interactive Setup Builder (#/quickstart)
export function renderQuickstart(main) {
  document.title = "快速开始" + DATA.copy.siteTitleSuffix;
  const builder = {
    agents: ["claude", "codex", "cursor"],
    claudeMode: "native",
    dryRun: true,
    shared: false,
    projectRules: false,
    importMode: "prompt" // prompt | import | no-import
  };

  main.innerHTML = `
    <section class="sub-sec" data-surface="sodium">
      <div class="grid">
        <p class="col-1-3 t-micro">(快速开始)</p>
        <div class="col-5-12">
          <h1 class="t-h1" tabindex="-1">三分钟安装，一键打通所有 AI 工具。</h1>
          <p class="t-body" style="margin-top:16px;">需要 Go 1.25+ 环境，纯标准库零依赖编译。跨平台支持 macOS、Linux 与 Windows。</p>
        </div>

        <div class="col-1-5" style="margin-top:40px;">
          <p class="t-micro">(步骤 01 · 编译安装)</p>
          <h2 class="t-lead" style="margin-top:8px;">获取单文件可执行程序</h2>
          <p class="t-small" style="margin-top:12px;">安装后请确保 <code class="t-mono">$GOPATH/bin</code> 已加入系统 <code class="t-mono">PATH</code>。</p>
        </div>
        <div class="col-7-12" style="margin-top:40px;">
          <div class="code-header">
            <span class="t-micro fw-600">从源码安装 (Go 1.25+)</span>
            <button type="button" class="cmd-copy-btn" id="qs-copy-install">复制</button>
          </div>
          <pre class="code-block" id="qs-install-code">git clone https://github.com/evepupil/SMem.git
cd SMem
go install ./cmd/smem</pre>
        </div>

        <div class="col-1-5" style="margin-top:48px;">
          <p class="t-micro">(步骤 02 · 交互式命令生成器)</p>
          <h2 class="t-lead" style="margin-top:8px;">定制你的 <code class="t-mono">smem setup</code></h2>
          <p class="t-small" style="margin-top:12px;">勾选正在使用的 AI 编程工具与协作选项，右侧即时生成可直接运行的接入命令。</p>
        </div>
        <div class="col-7-12" style="margin-top:48px;">
          <div style="border:2px solid var(--ink);background:var(--paper);padding:20px;">
            <p class="t-micro fw-600">选择要接入的客户端 (--agents)</p>
            <div style="display:flex;flex-wrap:wrap;gap:8px;margin-top:10px;" id="qs-agent-toggles">
              ${DATA.clients.map(cl => `
                <button type="button" class="chip-toggle t-micro" data-ag-id="${esc(cl.flag)}" aria-pressed="${builder.agents.includes(cl.flag) ? "true" : "false"}">${esc(cl.name)}</button>
              `).join("")}
            </div>

            <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:16px;margin-top:20px;">
              <div>
                <p class="t-micro fw-600">Claude Code 协同模式</p>
                <div class="seg-group" style="margin-top:8px;" id="qs-claude-mode">
                  <button type="button" class="chip-toggle t-micro" data-cm="native" aria-pressed="true">native (推荐)</button>
                  <button type="button" class="chip-toggle t-micro" data-cm="managed" aria-pressed="false">managed</button>
                </div>
              </div>
              <div>
                <p class="t-micro fw-600">历史记忆导入策略</p>
                <div class="seg-group" style="margin-top:8px;" id="qs-import-mode">
                  <button type="button" class="chip-toggle t-micro" data-im="prompt" aria-pressed="true">交互确认</button>
                  <button type="button" class="chip-toggle t-micro" data-im="import" aria-pressed="false">--import</button>
                  <button type="button" class="chip-toggle t-micro" data-im="no-import" aria-pressed="false">--no-import</button>
                </div>
              </div>
            </div>

            <div style="display:flex;flex-wrap:wrap;gap:8px;margin-top:20px;" id="qs-flags">
              <button type="button" class="chip-toggle t-micro" data-fl="dryRun" aria-pressed="true">--dry-run (先预览不写盘)</button>
              <button type="button" class="chip-toggle t-micro" data-fl="shared" aria-pressed="false">--shared (随仓库共享)</button>
              <button type="button" class="chip-toggle t-micro" data-fl="projectRules" aria-pressed="false">--project-rules (生成 AGENTS.md)</button>
            </div>

            <div style="margin-top:24px;">
              <div class="code-header">
                <span class="t-micro fw-600">生成的接入命令</span>
                <button type="button" class="cmd-copy-btn" id="qs-copy-setup">复制命令</button>
              </div>
              <pre class="code-block" id="qs-setup-output"></pre>
            </div>
          </div>
        </div>
      </div>
    </section>

    <section class="sec" data-surface="paper" id="faq">
      <div class="grid">
        <p class="col-1-3 t-micro">(常见问题)</p>
        <div class="col-5-12">
          <h2 class="t-h2">关于仓库污染、并发安全与卸载的常见疑问。</h2>
          <div class="faq-list">
            ${DATA.faq.map((f, idx) => `
              <details class="faq-item"${idx === 0 ? " open" : ""}>
                <summary class="faq-summary t-lead">
                  <span>${esc(f.q)}</span>
                  <span class="faq-icon-plus">${svgPixelIcon("plus", 16)}</span>
                  <span class="faq-icon-minus">${svgPixelIcon("minus", 16)}</span>
                </summary>
                <p class="faq-answer t-body">${esc(f.a)}</p>
              </details>
            `).join("")}
          </div>
        </div>
      </div>
    </section>
  `;

  const outEl = document.getElementById("qs-setup-output");
  function updateCmd() {
    const parts = ["smem setup"];
    if (builder.agents.length === DATA.clients.length) {
      parts.push("--agents all");
    } else if (builder.agents.length > 0) {
      parts.push(`--agents ${builder.agents.join(",")}`);
    }
    if (builder.claudeMode === "managed") parts.push("--claude-mode managed");
    if (builder.shared) parts.push("--shared");
    if (builder.projectRules) parts.push("--project-rules");
    if (builder.importMode === "import") parts.push("--import");
    if (builder.importMode === "no-import") parts.push("--no-import");
    if (builder.dryRun) parts.push("--dry-run");
    outEl.textContent = parts.join(" ");
  }

  document.querySelectorAll("#qs-agent-toggles [data-ag-id]").forEach(btn => {
    btn.addEventListener("click", () => {
      const id = btn.dataset.agId;
      const i = builder.agents.indexOf(id);
      if (i === -1) builder.agents.push(id);
      else builder.agents.splice(i, 1);
      btn.setAttribute("aria-pressed", i === -1 ? "true" : "false");
      updateCmd();
    });
  });

  document.querySelectorAll("#qs-claude-mode [data-cm]").forEach(btn => {
    btn.addEventListener("click", () => {
      builder.claudeMode = btn.dataset.cm;
      document.querySelectorAll("#qs-claude-mode [data-cm]").forEach(b => b.setAttribute("aria-pressed", b.dataset.cm === builder.claudeMode ? "true" : "false"));
      updateCmd();
    });
  });

  document.querySelectorAll("#qs-import-mode [data-im]").forEach(btn => {
    btn.addEventListener("click", () => {
      builder.importMode = btn.dataset.im;
      document.querySelectorAll("#qs-import-mode [data-im]").forEach(b => b.setAttribute("aria-pressed", b.dataset.im === builder.importMode ? "true" : "false"));
      updateCmd();
    });
  });

  document.querySelectorAll("#qs-flags [data-fl]").forEach(btn => {
    btn.addEventListener("click", () => {
      const k = btn.dataset.fl;
      builder[k] = !builder[k];
      btn.setAttribute("aria-pressed", builder[k] ? "true" : "false");
      updateCmd();
    });
  });

  document.getElementById("qs-copy-install").addEventListener("click", e => {
    copyText(document.getElementById("qs-install-code").textContent, e.currentTarget);
  });
  document.getElementById("qs-copy-setup").addEventListener("click", e => {
    copyText(outEl.textContent, e.currentTarget);
  });

  updateCmd();
}

// Route 6: Interactive Memory Card & Secret Guard Sandbox (#/sandbox)
export function renderSandbox(main, runtime) {
  document.title = "记忆工坊" + DATA.copy.siteTitleSuffix;

  const form = {
    type: "project",
    name: "生产发版审批人约定",
    desc: "涉及生产环境发布、上线交接单或部署审批流程时触发",
    body: "本项目每次生产发版前必须由小周审批放行，再执行 `scripts/deploy.sh`。\n\n<private>临时排查账号密码不会落盘</private>\n\n**Why:** 2026-11-23 起由小周接替原负责人把关 checkout 服务生产发布。\n**How to apply:** 准备上线交接单或部署步骤时，固定填写小周为放行人。"
  };

  let grid = runtime.state.stampGrid.split("");
  let currentColor = "3";
  let painting = false;

  main.innerHTML = `
    <section class="sub-sec" data-surface="ink">
      <div class="grid">
        <p class="col-1-3 t-micro">(记忆工坊 & 密钥守卫沙箱)</p>
        <div class="col-5-12">
          <h1 class="t-h1 t-paper" tabindex="-1">在线编写 SMem 主题文件，实测 11 类密钥拦截。</h1>
          <p class="t-body t-paper" style="margin-top:16px;opacity:0.86;">试着在正文中粘贴一个模拟的 <code class="t-mono">sk-ant-...</code> 或 <code class="t-mono">ghp_...</code>，观察 <code class="t-mono">internal/guard</code> 实时阻断，以及 <code class="t-mono">&lt;private&gt;</code> 自动剥离效果。还可以绘制 16×16 像素印章导出记忆卡片。</p>
        </div>
      </div>

      <div class="grid badge-layout">
        <div class="col-1-6">
          <div style="display:flex;flex-direction:column;gap:14px;">
            <div>
              <label class="t-micro" for="sb-type">记忆类型 (type)</label>
              <select class="select-input" style="margin-top:6px;" id="sb-type">
                <option value="project" selected>project — 项目决策与约束</option>
                <option value="feedback">feedback — 纠错与工作流纪律</option>
                <option value="user">user — 开发者角色与偏好 (global)</option>
                <option value="reference">reference — 外部看板与系统指针</option>
              </select>
            </div>
            <div>
              <label class="t-micro" for="sb-name">主题名称 (name)</label>
              <input class="text-input" style="margin-top:6px;" id="sb-name" value="${esc(form.name)}">
            </div>
            <div>
              <label class="t-micro" for="sb-desc">触发条件 (description)</label>
              <input class="text-input" style="margin-top:6px;" id="sb-desc" value="${esc(form.desc)}">
            </div>
            <div>
              <div style="display:flex;justify-content:space-between;align-items:center;">
                <label class="t-micro" for="sb-body">正文内容 (Markdown)</label>
                <button type="button" class="btn-text t-micro u-anim" id="sb-inject-secret">(注入测试密钥触发拦截)</button>
              </div>
              <textarea class="textarea-input t-mono" style="margin-top:6px;min-height:150px;font-size:0.84rem;" id="sb-body">${esc(form.body)}</textarea>
            </div>
            <div id="sb-guard-status"></div>
          </div>

          <div style="margin-top:28px;padding-top:24px;border-top:var(--rule);">
            <p class="t-micro">(可选 · 16×16 项目像素印章)</p>
            <div style="display:flex;flex-wrap:wrap;gap:20px;align-items:flex-start;margin-top:12px;">
              <canvas class="badge-editor-canvas" id="stamp-canvas" style="max-width:224px;" role="img" aria-label="16×16 像素印章画板"></canvas>
              <div style="flex:1;min-width:160px;">
                <div class="palette-group" id="stamp-palette" style="margin-top:0;">
                  ${STAMP_PALETTE.map(p => `
                    <button type="button" class="palette-swatch" data-col="${p.id}" aria-checked="${p.id === currentColor ? "true" : "false"}" style="background:${p.hex}" title="${p.name}"></button>
                  `).join("")}
                </div>
                <div style="display:flex;flex-wrap:wrap;gap:8px;margin-top:14px;">
                  <button type="button" class="chip-toggle t-micro" id="stamp-random">(随机像素纹)</button>
                  <button type="button" class="chip-toggle t-micro" id="stamp-clear">(清空)</button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="col-7-12">
          <div class="code-header">
            <span class="t-micro fw-600" id="sb-filename-label">project_release_owner.md</span>
            <span class="t-micro t-mono" id="sb-etag-label">etag: calculating...</span>
          </div>
          <pre class="code-block" id="sb-md-preview" style="min-height:240px;"></pre>

          <div style="margin-top:20px;display:flex;flex-wrap:wrap;gap:12px;">
            <button type="button" class="btn-primary t-small" id="sb-dl-md"><span>下载标准 .md 文件</span></button>
            <button type="button" class="btn-outline t-small" id="sb-dl-svg">导出像素记忆卡 (.svg)</button>
          </div>

          <svg id="sb-card-svg" viewBox="0 0 420 220" shape-rendering="crispEdges" style="display:block;width:100%;max-width:460px;height:auto;margin-top:20px;border:1px solid var(--sodium);"></svg>
        </div>
      </div>
    </section>
  `;

  const typeEl = document.getElementById("sb-type");
  const nameEl = document.getElementById("sb-name");
  const descEl = document.getElementById("sb-desc");
  const bodyEl = document.getElementById("sb-body");
  const guardEl = document.getElementById("sb-guard-status");
  const mdPrevEl = document.getElementById("sb-md-preview");
  const fnameEl = document.getElementById("sb-filename-label");
  const etagEl = document.getElementById("sb-etag-label");
  const cardSvg = document.getElementById("sb-card-svg");
  const stampCanvas = document.getElementById("stamp-canvas");

  function computePseudoEtag(str) {
    let h1 = 0x811c9dc5, h2 = 0x9e3779b9;
    for (let i = 0; i < str.length; i++) {
      h1 = Math.imul(h1 ^ str.charCodeAt(i), 0x01000193) >>> 0;
      h2 = Math.imul(h2 ^ (str.charCodeAt(i) + i), 0x85ebca6b) >>> 0;
    }
    return (h1.toString(16).padStart(8, "0") + h2.toString(16).padStart(4, "0")).slice(0, 12);
  }

  function renderStampCanvas() {
    const size = 224;
    const cell = 14;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    stampCanvas.width = size * dpr;
    stampCanvas.height = size * dpr;
    const ctx = stampCanvas.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.fillStyle = DATA.tokens.ink;
    ctx.fillRect(0, 0, size, size);
    for (let r = 0; r < 16; r++) {
      for (let c = 0; c < 16; c++) {
        const palObj = STAMP_PALETTE[Number(grid[r * 16 + c])] || STAMP_PALETTE[1];
        ctx.fillStyle = palObj.hex;
        ctx.fillRect(c * cell + 1, r * cell + 1, cell - 1, cell - 1);
      }
    }
  }

  function updateAll() {
    form.type = typeEl.value;
    form.name = nameEl.value.trim() || "未命名记忆";
    form.desc = descEl.value.trim() || "触发条件描述";
    form.body = bodyEl.value;

    const scan = scanSecrets(form.name + "\n" + form.desc + "\n" + form.body);
    const mdText = `---\nname: ${form.name}\ndescription: ${form.desc}\ntype: ${form.type}\n---\n\n${scan.sanitized}\n`;
    const slug = `${form.type}_memory_entry.md`;
    const etag = computePseudoEtag(mdText);

    fnameEl.textContent = slug;
    etagEl.textContent = `etag: ${etag}`;
    mdPrevEl.textContent = mdText;

    if (scan.findings.length > 0) {
      guardEl.innerHTML = `<div class="guard-banner t-small">internal/guard 已拦截写入：检测到高置信度凭证（${esc(scan.findings.join("、"))}）。MCP 模式下禁止落盘。</div>`;
    } else if (scan.hasPrivateTag) {
      guardEl.innerHTML = `<div class="guard-banner is-ok t-small">guard 校验通过：已自动剥离 &lt;private&gt;...&lt;/private&gt; 敏感片段，可安全写入。</div>`;
    } else {
      guardEl.innerHTML = `<div class="guard-banner is-ok t-small">guard 校验通过：未命中 11 类密钥特征，允许原子落盘。</div>`;
    }

    // Update SVG card preview
    const logoLayout = layoutGlyphLines(["SMEM"], 2);
    let logoRects = "";
    for (let i = 0; i < logoLayout.cells.length; i++) {
      const cell = logoLayout.cells[i];
      logoRects += `<rect x="${(14 + cell.x * 2.2).toFixed(1)}" y="${(10 + cell.y * 1.8).toFixed(1)}" width="2.2" height="1.8" fill="${DATA.tokens.sodium}"/>`;
    }
    let stampRects = "";
    for (let r = 0; r < 16; r++) {
      for (let c = 0; c < 16; c++) {
        const palObj = STAMP_PALETTE[Number(grid[r * 16 + c])] || STAMP_PALETTE[1];
        stampRects += `<rect x="${20 + c * 7}" y="${58 + r * 7}" width="7" height="7" fill="${palObj.hex}"/>`;
      }
    }
    let strip = "";
    for (let i = 0; i < 21; i++) {
      strip += `<rect x="${i * 20}" y="204" width="20" height="16" fill="${TRAIL_COLORS[i % TRAIL_COLORS.length]}"/>`;
    }
    cardSvg.innerHTML = `
      <rect x="0" y="0" width="420" height="220" fill="${DATA.tokens.sodium}"/>
      <rect x="0" y="0" width="420" height="38" fill="${DATA.tokens.ink}"/>
      ${logoRects}
      <text x="404" y="24" text-anchor="end" font-family="JetBrains Mono, monospace" font-size="11" fill="${DATA.tokens.sodium}">type: ${esc(form.type)} · etag: ${etag}</text>
      ${stampRects}
      <text x="150" y="84" font-family="Inter Tight, Noto Sans SC, sans-serif" font-size="20" font-weight="700" fill="${DATA.tokens.ink}">${esc(form.name.slice(0, 16))}</text>
      <text x="150" y="110" font-family="Inter Tight, Noto Sans SC, sans-serif" font-size="12" fill="${DATA.tokens.ink}">${esc(form.desc.slice(0, 26))}</text>
      <text x="150" y="140" font-family="JetBrains Mono, monospace" font-size="11" fill="${DATA.tokens.ink}">Guard: ${scan.findings.length > 0 ? "BLOCKED (" + scan.findings.length + ")" : "PASS · 0 SECRETS"}</text>
      <text x="150" y="162" font-family="JetBrains Mono, monospace" font-size="11" fill="${DATA.tokens.ink}">~/.smem/projects/&lt;repo-id&gt;/</text>
      ${strip}
    `;
  }

  [typeEl, nameEl, descEl, bodyEl].forEach(el => el.addEventListener("input", updateAll));

  document.getElementById("sb-inject-secret").addEventListener("click", () => {
    if (!bodyEl.value.includes("sk-ant-api03-")) {
      bodyEl.value += "\n\nANTHROPIC_API_KEY=sk-ant-api03-demoBlockedTestKey123456";
      updateAll();
    }
  });

  function paintAt(e) {
    const rect = stampCanvas.getBoundingClientRect();
    const c = Math.min(15, Math.max(0, Math.floor(((e.clientX - rect.left) / rect.width) * 16)));
    const r = Math.min(15, Math.max(0, Math.floor(((e.clientY - rect.top) / rect.height) * 16)));
    grid[r * 16 + c] = currentColor;
    runtime.state.stampGrid = grid.join("");
    renderStampCanvas();
    updateAll();
  }
  stampCanvas.addEventListener("pointerdown", e => { e.preventDefault(); painting = true; paintAt(e); });
  stampCanvas.addEventListener("pointermove", e => { if (painting) paintAt(e); });
  window.addEventListener("pointerup", () => { painting = false; });

  document.querySelectorAll("#stamp-palette [data-col]").forEach(btn => {
    btn.addEventListener("click", () => {
      currentColor = btn.dataset.col;
      document.querySelectorAll("#stamp-palette [data-col]").forEach(b => b.setAttribute("aria-checked", b.dataset.col === currentColor ? "true" : "false"));
    });
  });

  document.getElementById("stamp-random").addEventListener("click", () => {
    const rng = makeRng(Date.now());
    const accent = String(3 + Math.floor(rng() * 6));
    const g = new Array(256).fill("1");
    for (let r = 2; r <= 13; r++) {
      for (let c = 0; c < 8; c++) {
        if (rng() < 0.44) { g[r * 16 + c] = accent; g[r * 16 + (15 - c)] = accent; }
      }
    }
    grid = g;
    runtime.state.stampGrid = grid.join("");
    renderStampCanvas();
    updateAll();
  });

  document.getElementById("stamp-clear").addEventListener("click", () => {
    grid = new Array(256).fill("1");
    runtime.state.stampGrid = grid.join("");
    renderStampCanvas();
    updateAll();
  });

  document.getElementById("sb-dl-md").addEventListener("click", () => {
    const scan = scanSecrets(form.name + "\n" + form.desc + "\n" + form.body);
    if (scan.findings.length > 0) return;
    const blob = new Blob([mdPrevEl.textContent], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = fnameEl.textContent;
    document.body.appendChild(a);
    a.click();
    a.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  });

  document.getElementById("sb-dl-svg").addEventListener("click", () => {
    const ser = new XMLSerializer();
    const clone = cardSvg.cloneNode(true);
    clone.setAttribute("xmlns", "http://www.w3.org/2000/svg");
    const blob = new Blob([`<?xml version="1.0" encoding="UTF-8"?>\n` + ser.serializeToString(clone)], { type: "image/svg+xml;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "smem-memory-card.svg";
    document.body.appendChild(a);
    a.click();
    a.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  });

  renderStampCanvas();
  updateAll();
}

// 404 Route
export function renderNotFound(main) {
  document.title = "页面未找到" + DATA.copy.siteTitleSuffix;
  const ch = window.innerWidth >= 768 ? 14 : 8;
  main.innerHTML = `
    <section class="sub-sec" data-surface="sodium">
      <div class="grid">
        <div class="col-full">
          ${svgBitmapText("404", ch, DATA.tokens.ink, { className: "notfound-bitmap", ariaHidden: true, bold: true })}
          <h1 class="t-h1" tabindex="-1">未找到对应路由。</h1>
          <div style="margin-top:32px;">
            <a class="btn-text t-lead u-anim" href="#/">(返回 SMem 首页)</a>
          </div>
        </div>
      </div>
    </section>
  `;
}
