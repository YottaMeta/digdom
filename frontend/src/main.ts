import './style.css';
import './app.css';

import {
    WindowMinimise,
    WindowToggleMaximise,
    WindowIsMaximised,
    Quit,
    EventsOn,
} from '../wailsjs/runtime/runtime';
import {
    GetDictWords, PickDict, StartScan, StopScan, Version,
    ListScans, LoadScanResults, UpdateReview, DiffScans, RecheckBatch,
    OpenURL, DeleteScanRecord, DeleteResultRecord, DeleteResults,
} from '../wailsjs/go/main/App';

const DEFAULT_DNS = '8.8.8.8,1.1.1.1,223.5.5.5,114.114.114.114';

const DNS_PRESETS = [
    {label: '默认', dns: DEFAULT_DNS},
    {label: '国内', dns: '223.5.5.5,119.29.29.29,114.114.114.114'},
    {label: '国外', dns: '8.8.8.8,1.1.1.1'},
];

// ---------- 类型 ----------

// 与 Wails 生成绑定模型的字段保持一致（宽松 string，避免字面量联合冲突）。
type Tag = string;

interface Result {
    name: string;
    ips: string[];
    cnames: string[];
    tag: Tag;
    base: string;
    depth: number;
}

interface Progress {
    queried: number;
    hits: number;
    wildcards: number;
    unreviewed: number;
    active: boolean;
}

interface Stats {
    queried: number;
    hits: number;
    wildcards: number;
    unreviewed: number;
    duration_ms: number;
    error: string;
}

// 历史列表条目的最小字段（与后端 ScanSummary 兼容）。
interface HistoryEntry {
    id: number;
    target: string;
    hits: number;
    queried: number;
    status: string;
    started_at: number;
}

// 表格行统一视图模型：实时 / 历史 / diff 三种来源归一。
interface RowVM {
    id: number;
    scanId: number;
    name: string;
    ips: string[];
    cnames: string[];
    tag: Tag;
    base: string;
    depth: number;
    verdict: string;
    note: string;
    httpStatus: number;
    httpScheme: string;
    httpOK: boolean;
    diffState?: string;
}

const THEME_KEY = 'digdom-theme';
const DARK = 'dark';
const LIGHT = 'light';

const TAG_LABEL: Record<string, string> = {
    hit: '命中',
    wildcard: '通配符',
    unreviewed: '待复核',
};

const VERDICT_LABEL: Record<string, string> = {
    '': '未复核',
    confirmed: '已确认',
    false: '已误报',
};

// ---------- 全局状态 ----------

let liveResults: Result[] = [];
let viewRows: RowVM[] = [];
let viewMode: 'live' | 'history' | 'diff' = 'live';
let currentFilter: 'all' | Tag = 'all';
let scanActive = false;
let historyList: HistoryEntry[] = [];
let activeHistoryId: number | null = null;
let selectedForDiff: number[] = [];
let diffPair: [number, number] | null = null;
// 批量复核勾选的行（key = `${scanId}:${name}`）。
let recheckSel = new Set<string>();

const $ = <T extends HTMLElement>(id: string): T => document.getElementById(id.replace(/^#/, '')) as T;

// 右键菜单项。
interface CtxItem {
    label?: string;
    action?: () => void;
    sep?: boolean;
}

// 调试输出已随 Phase 2 移除（此前遮挡左下角）。保留空实现使调用点无害。
function dbg(_s: string): void { /* noop */ }
dbg('module loaded');

void Version().then((v) => dbg(`BUILD ${v}`));

// 安全存储：某些 WebView2 配置下 wails:// 源会拒绝 localStorage，降级为内存。
const store = {
    get(k: string): string | null {
        try { return localStorage.getItem(k); } catch { return null; }
    },
    set(k: string, v: string): void {
        try { localStorage.setItem(k, v); } catch { /* ignore */ }
    },
};

// 全局错误显形：任何 JS 异常都写进状态栏，避免"点了没反应"。
window.addEventListener('error', (e) => {
    try {
        ($('#status-text')).textContent = `JS 错误：${e.message}`;
        dbg(`error: ${e.message}`);
    } catch { /* ignore */ }
});
window.addEventListener('unhandledrejection', (e) => {
    try {
        ($('#status-text')).textContent = `未处理异常：${String(e.reason)}`;
        dbg(`unhandled: ${String(e.reason)}`);
    } catch { /* ignore */ }
});

// ---------- 主题 ----------

function currentTheme(): string {
    return document.documentElement.getAttribute('data-theme') === LIGHT ? LIGHT : DARK;
}

function applyTheme(theme: string): void {
    document.documentElement.setAttribute('data-theme', theme);
    const btn = document.getElementById('theme-toggle');
    if (btn) {
        btn.textContent = theme === DARK ? '☀️' : '🌙';
        btn.title = theme === DARK ? '切换到亮色' : '切换到暗色';
    }
    store.set(THEME_KEY, theme);
}

// ---------- 渲染 ----------

const ICON_MIN = '<svg width="12" height="12" viewBox="0 0 12 12"><line x1="1" y1="6" x2="11" y2="6" stroke="currentColor" stroke-width="1"/></svg>';
const ICON_MAX = '<svg width="12" height="12" viewBox="0 0 12 12"><rect x="1" y="1" width="10" height="10" rx="1" fill="none" stroke="currentColor" stroke-width="1"/></svg>';
const ICON_RESTORE = '<svg width="12" height="12" viewBox="0 0 12 12"><rect x="1" y="3" width="8" height="8" rx="1" fill="none" stroke="currentColor" stroke-width="1"/><path d="M3 3V1.5A.5.5 0 0 1 3.5 1h7a.5.5 0 0 1 .5.5v7a.5.5 0 0 1-.5.5H10" fill="none" stroke="currentColor" stroke-width="1"/></svg>';
const ICON_CLOSE = '<svg width="12" height="12" viewBox="0 0 12 12"><line x1="2" y1="2" x2="10" y2="10" stroke="currentColor" stroke-width="1.2"/><line x1="10" y1="2" x2="2" y2="10" stroke="currentColor" stroke-width="1.2"/></svg>';

function renderLayout(): void {
    const app = document.getElementById('app');
    if (!app) return;
    app.innerHTML = `
        <header class="topbar">
            <span class="brand">域探 · DigDom</span>
            <div class="controls">
                <button id="btn-help" class="ctl-btn" type="button" title="帮助 / 使用说明">?</button>
                <button id="theme-toggle" class="ctl-btn theme-btn" type="button" title="切换亮暗主题"></button>
                <button id="btn-min" class="ctl-btn win-btn" type="button" title="最小化">${ICON_MIN}</button>
                <button id="btn-max" class="ctl-btn win-btn" type="button" title="最大化">${ICON_MAX}</button>
                <button id="btn-close" class="ctl-btn win-btn win-btn-close" type="button" title="关闭">${ICON_CLOSE}</button>
            </div>
        </header>

        <section class="params">
            <div class="prow">
                <label for="target">目标</label>
                <input id="target" type="text" placeholder="example.com" spellcheck="false"/>
                <label for="depth">递归深度</label>
                <select id="depth" title="0=只爆一层，1/2=递归下级">
                    <option value="0">0（单层）</option>
                    <option value="1">1</option>
                    <option value="2">2</option>
                </select>
                <button id="btn-start" class="btn" type="button">开始爆破</button>
                <button id="btn-stop" class="btn btn-stop" type="button" disabled>停止</button>
            </div>
            <div class="prow">
                <label for="concurrency">并发</label>
                <input type="range" id="concurrency-range" min="1" max="2000" step="50" value="300"/>
                <input type="number" id="concurrency" value="300" min="1" max="2000" step="50"/>
                <label for="rps">限速/秒</label>
                <input type="range" id="rps-range" min="0" max="2000" step="50" value="0"/>
                <input type="number" id="rps" value="0" min="0" max="2000" step="50" placeholder="0=不限"/>
                <label for="dns">DNS 服务器</label>
                <input id="dns" type="text" value="${DEFAULT_DNS}" spellcheck="false"/>
                <span class="presets" id="dns-presets">
                    ${DNS_PRESETS.map((p) => `<button type="button" class="chip" data-dns="${p.dns}">${p.label}</button>`).join('')}
                </span>
            </div>
            <div class="prow">
                <label for="custom-dict">自定义追加词</label>
                <textarea id="custom-dict" placeholder="单词之间用 空格/逗号/换行 分隔；与所选字典合并去重"></textarea>
                <label for="dict">字典文件</label>
                <input id="dict" type="text" readonly placeholder="（默认字典 dic.txt，在程序目录下可直接编辑）"/>
                <button id="btn-browse" class="btn btn-ghost" type="button">浏览…</button>
                <span id="dict-note" class="dict-note"></span>
            </div>
        </section>

        <section class="body">
            <aside class="sidebar">
                <div class="side-head">
                    <b>历史扫描</b>
                    <button id="btn-live" class="btn btn-ghost btn-sm" type="button">当前结果</button>
                    <button id="btn-refresh" class="btn btn-ghost btn-sm" type="button">刷新</button>
                </div>
                <div class="side-list" id="side-list"><div class="side-empty">暂无历史，跑一次爆破后自动落库</div></div>
                <div class="side-foot">
                    <button id="btn-diff" class="btn btn-sm" type="button" disabled>对比所选</button>
                    <span id="diff-count" class="dict-note">0/2</span>
                </div>
            </aside>
            <div class="left">
                <div class="toolbar">
                    <span>结果 <b id="count">0</b></span>
                    <span id="mode-label" class="mode-label">实时</span>
                    <select id="filter">
                        <option value="all">全部</option>
                        <option value="hit">仅命中</option>
                        <option value="wildcard">仅通配符</option>
                        <option value="unreviewed">仅待复核</option>
                    </select>
                    <span class="spacer"></span>
                    <button id="btn-recheck" class="btn btn-ghost" type="button">批量复核</button>
                    <button id="btn-del-checked" class="btn btn-ghost" type="button">批量删除</button>
                    <button id="btn-export" class="btn btn-ghost" type="button">导出 CSV</button>
                </div>
                <div class="results-wrap">
                    <table class="results">
                        <thead>
                        <tr><th class="chk-col"><input type="checkbox" id="chk-all" title="全选当前可见行"/></th><th>域名</th><th>IP / CNAME</th><th>标签</th><th>探测</th><th>深度</th></tr>
                        </thead>
                        <tbody id="tbody"></tbody>
                    </table>
                </div>
            </div>
            <aside class="detail">
                <h3>详情</h3>
                <div id="detail-body"><span class="empty">点击左侧行查看详情</span></div>
            </aside>
        </section>

        <footer class="status">
            <div class="prog"><div id="bar" class="bar"></div></div>
            <span id="status-text">就绪</span>
            <span>查询 <b id="q">0</b></span>
            <span>命中 <b id="h">0</b></span>
            <span>通配符 <b id="w">0</b></span>
            <span>待复核 <b id="u">0</b></span>
        </footer>
        <div id="ctx-menu" class="ctx-menu"></div>
        <div id="toast" class="toast"></div>
        <div id="confirm-ovl" class="confirm-ovl">
            <div class="confirm-box">
                <div id="confirm-msg" class="confirm-msg"></div>
                <div class="confirm-btns">
                    <button id="confirm-cancel" class="btn" type="button">取消</button>
                    <button id="confirm-ok" class="btn btn-danger" type="button">确定</button>
                </div>
            </div>
        </div>
        <div id="help-ovl" class="help-ovl">
            <div class="help-box">
                <div class="help-head">
                    <b>域探 · DigDom 使用说明</b>
                    <button id="help-close" class="ctl-btn" type="button" title="关闭">×</button>
                </div>
                <div class="help-body">
                    <section>
                        <h4>一、工作流程</h4>
                        <p>1. 顶部参数栏填目标域名 → 2. 调整参数 → 3. 点「开始爆破」→ 4. 结果实时出现在右侧表格。每次扫描结束会自动存入左侧「历史扫描」。</p>
                    </section>
                    <section>
                        <h4>二、爆破参数</h4>
                        <dl>
                            <dt><b>目标</b></dt><dd>要爆破的主域名，如 <code>example.com</code>（不带 www/http）。</dd>
                            <dt><b>递归深度</b></dt><dd>是否继续爆破命中的子域名（如对 <code>api</code> 再爆 <code>x.api</code>）。0 只爆第一层，越大越耗时。</dd>
                            <dt><b>并发</b></dt><dd>同时发起的 DNS 查询数。越大越快，也越占 CPU/带宽。内网或自建 DNS 可调高，公网建议保守。</dd>
                            <dt><b>限速/秒</b></dt><dd>每秒最多发起的查询数（0 = 不限）。主要用来控制对目标 DNS 的冲击，防封 IP。民用目标建议 50–200。</dd>
                            <dt><b>DNS 服务器</b></dt><dd>查询用的 DNS。点右侧预设 chip 可一键填常用公共 DNS。</dd>
                            <dt><b>字典</b></dt><dd>爆破用的单词表。可「浏览」选文件，或在「自定义追加词」里临时加词（自动与原字典合并去重）。默认取程序目录 dic.txt。</dd>
                        </dl>
                    </section>
                    <section>
                        <h4>三、结果列表</h4>
                        <dl>
                            <dt><b>标签</b></dt><dd>hit=命中（有解析），wildcard=通配符（DNS 泛解析干扰，多为假命中），unreviewed=待处理。</dd>
                            <dt><b>探测</b></dt><dd>HTTP 探活结果（状态码 / 不可达 / 未探活），由「批量复核」写入。</dd>
                            <dt><b>深度</b></dt><dd>该域名在第几级被爆破到。</dd>
                            <dt><b>筛选</b></dt><dd>按标签过滤显示结果。</dd>
                            <dt><b>全选</b></dt><dd>表头勾选框，选中所有可见行。</dd>
                        </dl>
                        <p><b>右键某一行：</b>打开域名 / 复制域名 / IP / CNAME / 整行；历史模式另可「探活该条」「删除该条」。</p>
                    </section>
                    <section>
                        <h4>四、历史 / 复核 / 对比</h4>
                        <dl>
                            <dt><b>历史扫描</b></dt><dd>点开一条看当时结果。右键历史可重开或删除。</dd>
                            <dt><b>批量复核</b></dt><dd>勾选若干行后点它，或用 HTTP 探活逐个验证；可达自动标「确认真实存在」并记录探测，不可达仅记录。不勾选则处理当前历史全部。</dd>
                            <dt><b>对比所选</b></dt><dd>勾选两条历史后点此，用 diff 对比两次扫描的资产变化（新增绿 / 消失红）。</dd>
                            <dt><b>复核</b></dt><dd>在右侧详情栏给单挑标「确认真实存在 / 确认误报」并加备注。</dd>
                        </dl>
                    </section>
                    <section>
                        <h4>五、其它</h4>
                        <dl>
                            <dt><b>批量删除</b></dt><dd>删除当前勾选的历史结果（有确认，不可恢复）。</dd>
                            <dt><b>导出 CSV</b></dt><dd>把当前筛选的结果导出为 CSV 文件。</dd>
                            <dt><b>命令行（CLI）</b></dt><dd>同目录 <code>digdom-cli.exe</code>：<code>-target example.com</code> 扫描落库，<code>history</code> / <code>diff a b</code> 查看对比，与 GUI 共用历史。</dd>
                            <dt><b>亮暗主题</b></dt><dd>点顶栏主题按钮切换；明暗色更适合暗光环境。</dd>
                        </dl>
                    </section>
                </div>
            </div>
        </div>
    `;
}

// ---------- 右键菜单 ----------

function showCtxMenu(x: number, y: number, items: CtxItem[]): void {
    const menu = $('#ctx-menu');
    menu.innerHTML = '';
    for (const it of items) {
        if (it.sep) {
            const s = document.createElement('div');
            s.className = 'ctx-sep';
            menu.appendChild(s);
            continue;
        }
        const b = document.createElement('div');
        b.className = 'ctx-item';
        b.textContent = it.label || '';
        b.addEventListener('click', () => {
            hideCtxMenu();
            it.action?.();
        });
        menu.appendChild(b);
    }
    menu.style.display = 'block';
    // 越界收拢到窗口内
    const r = menu.getBoundingClientRect();
    const vw = window.innerWidth, vh = window.innerHeight;
    const left = Math.min(x, vw - r.width - 8);
    const top = Math.min(y, vh - r.height - 8);
    menu.style.left = `${Math.max(4, left)}px`;
    menu.style.top = `${Math.max(4, top)}px`;
}

function hideCtxMenu(): void {
    const menu = document.getElementById('ctx-menu');
    if (menu) menu.style.display = 'none';
}

// ---------- 自定义提示（Toast） ----------
let toastTimer: number | null = null;
function showToast(msg: string, type: 'info' | 'warn' = 'info'): void {
    const t = $('#toast');
    t.textContent = msg;
    t.className = 'toast show ' + type;
    if (toastTimer != null) window.clearTimeout(toastTimer);
    toastTimer = window.setTimeout(() => t.classList.remove('show'), 2600);
}

// ---------- 自定义确认弹窗（替代原生 confirm） ----------
let confirmCb: (() => void) | null = null;
function showConfirm(msg: string, onOk: () => void): void {
    confirmCb = onOk;
    $('#confirm-msg').textContent = msg;
    $('#confirm-ovl').classList.add('show');
}
function hideConfirm(): void {
    $('#confirm-ovl').classList.remove('show');
    confirmCb = null;
}
function bindConfirm(): void {
    document.getElementById('confirm-cancel')!.addEventListener('click', hideConfirm);
    document.getElementById('confirm-ok')!.addEventListener('click', () => {
        const cb = confirmCb;
        hideConfirm();
        if (cb) cb();
    });
}

// ---------- 结果表格 ----------

function verdictBadge(verdict: string): string {
    if (!verdict) return '';
    return `<span class="badge ${verdict === 'confirmed' ? 'verdict-ok' : 'verdict-no'}">${VERDICT_LABEL[verdict] || verdict}</span>`;
}

function rowForVM(vm: RowVM): HTMLTableRowElement {
    const tr = document.createElement('tr');
    if (vm.diffState) {
        tr.classList.add(vm.diffState === 'added' ? 'diff-add' : 'diff-rm');
    }
    tr.dataset.name = vm.name;
    tr.dataset.tag = vm.tag;
    if (vm.id) tr.dataset.id = String(vm.id);

    const selKey = vm.scanId + ':' + vm.name;
    const tdChk = document.createElement('td');
    tdChk.className = 'chk-col';
    const chk = document.createElement('input');
    chk.type = 'checkbox';
    chk.className = 'row-chk';
    chk.checked = recheckSel.has(selKey);
    chk.addEventListener('click', (e) => e.stopPropagation());
    chk.addEventListener('change', () => {
        if (chk.checked) recheckSel.add(selKey);
        else recheckSel.delete(selKey);
    });
    tdChk.appendChild(chk);

    const tdName = document.createElement('td');
    const prefix = vm.diffState ? (vm.diffState === 'added' ? '+ ' : '− ') : '';
    tdName.textContent = prefix + vm.name;
    tdName.title = vm.name;

    const tdIP = document.createElement('td');
    const ipSpan = document.createElement('span');
    ipSpan.className = 'ips';
    ipSpan.textContent = (vm.ips || []).join(', ') || ((vm.cnames || []).length ? (vm.cnames || []).join(', ') : '—');
    ipSpan.title = ipSpan.textContent;
    tdIP.appendChild(ipSpan);

    const tdTag = document.createElement('td');
    tdTag.innerHTML = `<span class="badge ${vm.tag}">${TAG_LABEL[vm.tag]}</span>${verdictBadge(vm.verdict)}`;
    tdTag.className = 'tag-cell';

    const tdProbe = document.createElement('td');
    tdProbe.className = 'probe-cell';
    if (vm.httpStatus > 0) {
        tdProbe.textContent = `${vm.httpStatus} ${vm.httpScheme}`;
        tdProbe.title = `HTTP 探活可达（${vm.httpScheme}）`;
        tdProbe.classList.add('probe-ok');
    } else if (vm.httpScheme === 'fail') {
        tdProbe.textContent = '不可达';
        tdProbe.title = 'HTTP 探活不可达';
        tdProbe.classList.add('probe-fail');
    } else {
        tdProbe.textContent = '—';
        tdProbe.title = '未探活（批量复核后显示）';
    }

    const tdDepth = document.createElement('td');
    tdDepth.textContent = vm.diffState ? `#${vm.scanId}` : String(vm.depth);

    tr.append(tdChk, tdName, tdIP, tdTag, tdProbe, tdDepth);
    tr.addEventListener('click', () => {
        if (vm.diffState) {
            void openHistory(vm.scanId);
        } else {
            selectRow(vm, tr);
        }
    });
    tr.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        e.stopPropagation();
        if (!vm.diffState) selectRow(vm, tr);
        showCtxMenu(e.clientX, e.clientY, ctxMenuItems(vm));
    });
    return tr;
}

// 结果行的右键菜单项。
function ctxMenuItems(vm: RowVM): CtxItem[] {
    const items: CtxItem[] = [
        {label: '打开域名', action: () => void openDomain(vm)},
        {label: '复制域名', action: () => void copyText(vm.name)},
        {label: '复制 IP', action: () => void copyText((vm.ips || []).join(', '))},
        {label: '复制 CNAME', action: () => void copyText((vm.cnames || []).join(', '))},
        {label: '复制整行（域\tIP\t标签）', action: () => void copyText([vm.name, (vm.ips || []).join(' '), vm.tag].join('\t'))},
    ];
    if (viewMode === 'history' && vm.id) {
        items.push(
            {sep: true},
            {label: '探活该条', action: () => void recheckOne(vm)},
            {label: '删除该条', action: () => void deleteResult(vm)},
        );
    }
    return items;
}

function renderTable(): void {
    const tbody = $('#tbody') as HTMLTableSectionElement;
    tbody.innerHTML = '';
    const frag = document.createDocumentFragment();
    for (const vm of viewRows) {
        if (currentFilter !== 'all' && vm.tag !== currentFilter) continue;
        frag.appendChild(rowForVM(vm));
    }
    tbody.appendChild(frag);
    const chkAll = $('#chk-all') as HTMLInputElement | null;
    if (chkAll) chkAll.checked = false;
    updateCount();
    updateModeLabel();
}

function appendResults(batch: Result[]): void {
    const tbody = $('#tbody') as HTMLTableSectionElement;
    const frag = document.createDocumentFragment();
    for (const r of batch) {
        liveResults.push(r);
        const vm: RowVM = {
            id: 0, scanId: 0, name: r.name, ips: r.ips || [], cnames: r.cnames || [],
            tag: r.tag, base: r.base, depth: r.depth, verdict: '', note: '',
            httpStatus: 0, httpScheme: '', httpOK: false,
        };
        viewRows.push(vm);
        if (currentFilter !== 'all' && r.tag !== currentFilter) continue;
        frag.appendChild(rowForVM(vm));
    }
    tbody.appendChild(frag);
    updateCount();
}

function currentCount(): number {
    if (currentFilter === 'all') return viewRows.length;
    let n = 0;
    for (const vm of viewRows) {
        if (vm.tag === currentFilter) n++;
    }
    return n;
}

function updateCount(): void {
    $('#count').textContent = String(currentCount());
}

function updateModeLabel(): void {
    const el = $('#mode-label');
    if (viewMode === 'live') el.textContent = '实时';
    else if (viewMode === 'history') el.textContent = `历史 #${activeHistoryId ?? ''}`;
    else if (diffPair) el.textContent = `对比 #${diffPair[0]}→#${diffPair[1]}`;
}

function selectRow(vm: RowVM, tr: HTMLTableRowElement): void {
    document.querySelectorAll('#tbody tr.selected').forEach((el) => el.classList.remove('selected'));
    tr.classList.add('selected');
    renderDetail(vm);
}

function renderDetail(vm: RowVM): void {
    const body = $('#detail-body');
    const ipList = (vm.ips || []).length ? (vm.ips || []).join('<br/>') : '（无 A/AAAA）';
    const cnList = (vm.cnames || []).length ? (vm.cnames || []).join('<br/>') : '（无 CNAME）';
    const verdict = vm.verdict ? `<span class="badge ${vm.verdict === 'confirmed' ? 'verdict-ok' : 'verdict-no'}">${VERDICT_LABEL[vm.verdict] || vm.verdict}</span>` : '<span class="empty">未复核</span>';
    let probe = '<span class="empty">未探活</span>';
    if (vm.httpStatus > 0) probe = `<span class="badge verdict-ok">${vm.httpStatus} ${vm.httpScheme}</span>`;
    else if (vm.httpScheme === 'fail') probe = '<span class="badge verdict-no">不可达</span>';
    const modeHint = vm.diffState === 'added' ? '该域名为本次对比新增，可在对应历史中复核' :
        vm.diffState === 'removed' ? '该域名本次对比已消失，可在对应历史中复核' : '';

    let reviewUI = '';
    if (viewMode === 'history' && vm.id) {
        reviewUI = `
            <div class="review-box">
                <h4>复核</h4>
                <select id="rv-verdict">
                    <option value="" ${vm.verdict === '' ? 'selected' : ''}>未复核</option>
                    <option value="confirmed" ${vm.verdict === 'confirmed' ? 'selected' : ''}>确认真实存在</option>
                    <option value="false" ${vm.verdict === 'false' ? 'selected' : ''}>确认误报</option>
                </select>
                <input id="rv-note" type="text" placeholder="备注（可选）" value="${escapeHtml(vm.note)}"/>
                <button id="btn-rv-save" class="btn btn-sm" type="button">保存复核</button>
            </div>
        `;
    }

    body.innerHTML = `
        <dl>
            <dt>域名</dt><dd>${escapeHtml(vm.name)}</dd>
            <dt>标签</dt><dd><span class="badge ${vm.tag}">${TAG_LABEL[vm.tag]}</span></dd>
            <dt>复核</dt><dd>${verdict}${vm.note ? ` <span class="note">${escapeHtml(vm.note)}</span>` : ''}</dd>
            <dt>HTTP 探活</dt><dd>${probe}</dd>
            <dt>IP</dt><dd>${ipList}</dd>
            <dt>CNAME</dt><dd>${cnList}</dd>
            <dt>归属层级</dt><dd>${escapeHtml(vm.base)}</dd>
            <dt>深度</dt><dd>${vm.depth}</dd>
        </dl>
        ${modeHint ? `<div class="empty" style="margin-top:8px">${modeHint}</div>` : ''}
        ${reviewUI}
    `;

    const saveBtn = document.getElementById('btn-rv-save');
    if (saveBtn) {
        saveBtn.addEventListener('click', () => void saveReview(vm));
    }
}

function escapeHtml(s: string): string {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// ---------- 历史 / 复核 / diff ----------

async function refreshHistory(): Promise<void> {
    try {
        historyList = await ListScans();
    } catch (err) {
        dbg(`ListScans 失败: ${String(err)}`);
        historyList = [];
    }
    renderHistoryList();
}

function renderHistoryList(): void {
    const list = $('#side-list');
    if (!historyList.length) {
        list.innerHTML = '<div class="side-empty">暂无历史，跑一次爆破后自动落库</div>';
        return;
    }
    const frag = document.createDocumentFragment();
    for (const sc of historyList) {
        const item = document.createElement('div');
        item.className = 'side-item' + (activeHistoryId === sc.id ? ' active' : '');
        item.dataset.id = String(sc.id);

        const chk = document.createElement('input');
        chk.type = 'checkbox';
        chk.className = 'side-chk';
        chk.dataset.id = String(sc.id);
        chk.checked = selectedForDiff.includes(sc.id);
        chk.addEventListener('click', (e) => e.stopPropagation());
        chk.addEventListener('change', () => onDiffSelect(Number(chk.dataset.id), chk.checked));

        const info = document.createElement('div');
        info.className = 'side-info';
        const t = document.createElement('div');
        t.className = 'side-target';
        t.textContent = sc.target;
        t.title = sc.target;
        const meta = document.createElement('div');
        meta.className = 'side-meta';
    const ts = new Date(sc.started_at);
    const tsStr = isNaN(ts.getTime()) ? '?' : `${String(ts.getMonth() + 1).padStart(2, '0')}-${String(ts.getDate()).padStart(2, '0')} ${String(ts.getHours()).padStart(2, '0')}:${String(ts.getMinutes()).padStart(2, '0')}`;
        meta.textContent = `${tsStr} · 命中 ${sc.hits} / ${sc.queried} 查`;
        if (sc.status === 'stopped') meta.textContent += ' · 已停止';
        info.append(t, meta);

        item.append(chk, info);
        item.addEventListener('click', () => void openHistory(sc.id));
        item.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            e.stopPropagation();
            showCtxMenu(e.clientX, e.clientY, [
                {label: `打开历史 #${sc.id}`, action: () => void openHistory(sc.id)},
                {sep: true},
                {label: `删除历史 #${sc.id}`, action: () => void deleteHistory(sc.id)},
            ]);
        });
        frag.appendChild(item);
    }
    list.innerHTML = '';
    list.appendChild(frag);
}

function onDiffSelect(id: number, checked: boolean): void {
    // 允许第三个？叶子箱：只允许选 2 个，超过用自定义提示阻止并还原勾选。
    if (checked && !selectedForDiff.includes(id) && selectedForDiff.length >= 2) {
        showToast('对比最多选 2 条历史，请先取消一条后再选', 'warn');
        // 还原该 checkbox
        const chk = document.querySelector(`.side-chk[data-id="${id}"]`) as HTMLInputElement | null;
        if (chk) chk.checked = false;
        return;
    }
    selectedForDiff = checked
        ? (selectedForDiff.includes(id) ? selectedForDiff : [...selectedForDiff, id])
        : selectedForDiff.filter((x) => x !== id);
    updateDiffUI();
    renderHistoryList();
}

function updateDiffUI(): void {
    const btn = $('#btn-diff') as HTMLButtonElement;
    btn.disabled = selectedForDiff.length !== 2;
    $('#diff-count').textContent = `${selectedForDiff.length}/2`;
}

async function openHistory(id: number): Promise<void> {
    try {
        const rows = await LoadScanResults(id);
        recheckSel.clear();
        activeHistoryId = id;
        viewMode = 'history';
        diffPair = null;
        viewRows = rows.map((r) => ({
            id: r.id, scanId: id, name: r.name, ips: r.ips || [], cnames: r.cnames || [],
            tag: r.tag, base: r.base, depth: r.depth, verdict: r.verdict, note: r.note,
            httpStatus: r.http_status || 0, httpScheme: r.http_scheme || '', httpOK: r.http_ok || false,
        }));
        currentFilter = 'all';
        ($('#filter') as HTMLSelectElement).value = 'all';
        renderTable();
        renderHistoryList();
        setStatus(`已加载历史 #${id}，共 ${viewRows.length} 条`);
    } catch (err) {
        setStatus(`加载历史失败：${String(err)}`);
    }
}

async function runDiff(): Promise<void> {
    if (selectedForDiff.length !== 2) {
        setStatus('请勾选两条历史后对比');
        return;
    }
    const [a, b] = [...selectedForDiff].sort((x, y) => x - y);
    try {
        const d = await DiffScans(a, b);
        recheckSel.clear();
        diffPair = [a, b];
        viewMode = 'diff';
        activeHistoryId = null;
        viewRows = [
            ...(d.added || []).map((x) => toDiffVM(x)),
            ...(d.removed || []).map((x) => toDiffVM(x)),
        ];
        currentFilter = 'all';
        ($('#filter') as HTMLSelectElement).value = 'all';
        renderTable();
        renderHistoryList();
        setStatus(`对比 #${a} → #${b}：新增 ${(d.added || []).length}，消失 ${(d.removed || []).length}`);
    } catch (err) {
        setStatus(`对比失败：${String(err)}`);
    }
}

function toDiffVM(x: { name: string; state: string; tag: string; ips: string[]; verdict: string; scan_id: number }): RowVM {
    return {
        id: 0, scanId: x.scan_id, name: x.name, ips: x.ips || [], cnames: [],
        tag: x.tag, base: '', depth: 0, verdict: x.verdict || '', note: '',
        httpStatus: 0, httpScheme: '', httpOK: false,
        diffState: x.state,
    };
}

async function saveReview(vm: RowVM): Promise<void> {
    const verdict = ($('#rv-verdict') as HTMLSelectElement).value;
    const note = ($('#rv-note') as HTMLInputElement).value.trim();
    if (!vm.id) {
        setStatus('该记录不可复核');
        return;
    }
    try {
        await UpdateReview(vm.scanId, vm.id, verdict, note);
        vm.verdict = verdict;
        vm.note = note;
        renderTable();
        renderDetail(vm);
        // 重渲染后重新高亮该行
        const tr = document.querySelector(`#tbody tr[data-id="${vm.id}"]`);
        if (tr) tr.classList.add('selected');
        setStatus(`已保存复核：${VERDICT_LABEL[verdict] || '未复核'}`);
    } catch (err) {
        setStatus(`保存复核失败：${String(err)}`);
    }
}

// 全选/取消全选当前可见行。
function onSelectAll(checked: boolean): void {
    const tbody = $('#tbody') as HTMLTableSectionElement;
    tbody.querySelectorAll<HTMLTableRowElement>('tr').forEach((tr) => {
        if (tr.style.display === 'none') return;
        const chk = tr.querySelector<HTMLInputElement>('.row-chk');
        const name = tr.dataset.name;
        if (chk && name) {
            chk.checked = checked;
            const key = `${activeHistoryId}:${name}`;
            if (checked) recheckSel.add(key);
            else recheckSel.delete(key);
        }
    });
}

// 批量复核：勾选了行则只处理勾选，否则处理当前历史全部。
async function recheckBatch(): Promise<void> {
    if (viewMode !== 'history' || activeHistoryId == null) {
        setStatus('批量复核仅对历史扫描生效（先点开一条历史）');
        return;
    }
    const names = viewRows
        .filter((vm) => vm.scanId === activeHistoryId && recheckSel.has(`${activeHistoryId}:${vm.name}`))
        .map((vm) => vm.name);
    const scope = names.length ? `勾选的 ${names.length} 条` : '全部结果';
    setStatus(`批量复核中（${scope}，HTTP 探活并发 50）…`);
    dbg(`RecheckBatch scan=${activeHistoryId} names=${names.length}`);
    try {
        const items = await RecheckBatch(activeHistoryId, names);
        const byName = new Map<string, {ok: boolean; note: string; status: number; scheme: string}>();
        let ok = 0;
        for (const it of items) {
            byName.set(it.name, {ok: it.ok, note: it.note, status: it.status, scheme: it.scheme});
            if (it.ok) ok++;
        }
        for (const vm of viewRows) {
            if (vm.scanId !== activeHistoryId) continue;
            const upd = byName.get(vm.name);
            if (!upd) continue;
            vm.verdict = upd.ok ? 'confirmed' : vm.verdict;
            vm.note = upd.note;
            vm.httpStatus = upd.status;
            vm.httpScheme = upd.scheme;
            vm.httpOK = upd.ok;
        }
        // 清掉本次已处理的勾选
        for (const k of Array.from(recheckSel)) {
            if (k.startsWith(`${activeHistoryId}:`)) recheckSel.delete(k);
        }
        renderTable();
        setStatus(`批量复核完成：${items.length} 条，可达 ${ok} 条（已标确认）`);
    } catch (err) {
        setStatus(`批量复核失败：${String(err)}`);
    }
}

function switchToLive(): void {
    recheckSel.clear();
    viewMode = 'live';
    activeHistoryId = null;
    diffPair = null;
    viewRows = liveResults.map((r) => ({
        id: 0, scanId: 0, name: r.name, ips: r.ips || [], cnames: r.cnames || [],
        tag: r.tag, base: r.base, depth: r.depth, verdict: '', note: '',
        httpStatus: 0, httpScheme: '', httpOK: false,
    }));
    renderTable();
    renderHistoryList();
    setStatus('已切回当前实时结果');
}

// ---------- 右键动作 ----------

async function copyText(text: string): Promise<void> {
    if (!text) {
        setStatus('没有可复制的内容');
        return;
    }
    try {
        await navigator.clipboard.writeText(text);
    } catch {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand('copy'); } catch { /* ignore */ }
        document.body.removeChild(ta);
    }
    setStatus(`已复制：${text.length > 40 ? text.slice(0, 40) + '…' : text}`);
}

function openDomain(vm: RowVM): void {
    const url = 'https://' + vm.name;
    void OpenURL(url).catch((err) => setStatus(`打开失败：${String(err)}`));
    setStatus(`正在打开 ${url}`);
}

async function recheckOne(vm: RowVM): Promise<void> {
    setStatus(`探活 ${vm.name} …`);
    try {
        const items = await RecheckBatch(vm.scanId, [vm.name]);
        const it = items[0];
        if (it) {
            if (it.ok) vm.verdict = 'confirmed';
            vm.note = it.note;
            vm.httpStatus = it.status;
            vm.httpScheme = it.scheme;
            vm.httpOK = it.ok;
        }
        renderTable();
        renderDetail(vm);
        setStatus(`探活完成：${vm.name} → ${it && it.ok ? '可达' : '不可达'}`);
    } catch (err) {
        setStatus(`探活失败：${String(err)}`);
    }
}

async function deleteResult(vm: RowVM): Promise<void> {
    showConfirm(`删除 ${vm.name}？此操作不可恢复。`, () => void doDeleteResult(vm));
}

// 批量删除：删除当前勾选的行（仅历史模式有效）。
async function batchDelete(): Promise<void> {
    if (viewMode !== 'history' || activeHistoryId == null) {
        showToast('批量删除仅对历史扫描生效（先点开一条历史）', 'warn');
        return;
    }
    const rows = viewRows.filter(
        (vm) => vm.scanId === activeHistoryId && vm.id && recheckSel.has(`${activeHistoryId}:${vm.name}`)
    );
    if (!rows.length) {
        showToast('未勾选任何行，请先勾选要删除的行', 'warn');
        return;
    }
    showConfirm(`确定删除勾选的 ${rows.length} 条记录？此操作不可恢复。`, () => void doBatchDelete(rows));
}

async function doBatchDelete(rows: RowVM[]): Promise<void> {
    const ids = rows.map((vm) => vm.id);
    try {
        await DeleteResults(activeHistoryId!, ids);
        const idSet = new Set(ids);
        viewRows = viewRows.filter((v) => !(v.scanId === activeHistoryId && idSet.has(v.id)));
        for (const k of Array.from(recheckSel)) {
            if (k.startsWith(`${activeHistoryId}:`)) recheckSel.delete(k);
        }
        renderTable();
    } catch (err) {
        setStatus(`批量删除失败：${String(err)}`);
        return;
    }
    setStatus(`已删除 ${rows.length} 条`);
}

async function doDeleteResult(vm: RowVM): Promise<void> {
    try {
        await DeleteResultRecord(vm.scanId, vm.id);
        viewRows = viewRows.filter((v) => !(v.scanId === vm.scanId && v.id === vm.id));
        renderTable();
    } catch (err) {
        setStatus(`删除失败：${String(err)}`);
        return;
    }
    setStatus(`已删除 ${vm.name}`);
}

async function deleteHistory(id: number): Promise<void> {
    showConfirm(`删除历史 #${id} 及其全部结果？此操作不可恢复。`, () => void doDeleteHistory(id));
}

async function doDeleteHistory(id: number): Promise<void> {
    try {
        await DeleteScanRecord(id);
        if (activeHistoryId === id) switchToLive();
        await refreshHistory();
    } catch (err) {
        setStatus(`删除失败：${String(err)}`);
        return;
    }
    setStatus(`已删除历史 #${id}`);
}

// ---------- 导出 ----------

function exportCSV(): void {
    const list = currentFilter === 'all' ? viewRows : viewRows.filter((r) => r.tag === currentFilter);
    if (!list.length) {
        setStatus('没有可导出的结果');
        return;
    }
    const header = 'domain,ip,tag,verdict,note,base,depth\n';
    const rows = list.map((r) =>
        [r.name, (r.ips || []).join(' '), r.tag, r.verdict, r.note, r.base, r.depth].map(escapeCSV).join(',')
    ).join('\n');
    const blob = new Blob(['\ufeff' + header + rows], {type: 'text/csv;charset=utf-8'});
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `digdom_${Date.now()}.csv`;
    a.click();
    URL.revokeObjectURL(a.href);
    setStatus(`已导出 ${list.length} 条`);
}

function escapeCSV(v: string | number): string {
    const s = String(v);
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

// ---------- 状态 ----------

function setStatus(text: string): void {
    ($('#status-text')).textContent = text;
    dbg(`状态: ${text}`);
}

function setProgress(p: Progress | null): void {
    const q = $('#q'), h = $('#h'), w = $('#w'), u = $('#u');
    if (p) {
        q.textContent = String(p.queried);
        h.textContent = String(p.hits);
        w.textContent = String(p.wildcards);
        u.textContent = String(p.unreviewed);
    } else {
        q.textContent = '0';
        h.textContent = '0';
        w.textContent = '0';
        u.textContent = '0';
    }
}

function setActive(active: boolean): void {
    scanActive = active;
    ($('#btn-start') as HTMLButtonElement).disabled = active;
    ($('#btn-stop') as HTMLButtonElement).disabled = !active;
}

// ---------- 扫描控制 ----------

async function startScan(): Promise<void> {
    dbg(`startScan 被触发 scanActive=${scanActive}`);
    if (scanActive) { dbg('→ 已有扫描进行中，本次点击被忽略'); return; }
    dbg(`目标=${($('#target') as HTMLInputElement).value.trim()}`);
    setStatus('准备开始…');
    const target = ($('#target') as HTMLInputElement).value.trim();
    if (!target) {
        setStatus('请输入目标子域');
        return;
    }
    const custom = ($('#custom-dict') as HTMLTextAreaElement).value;
    const depth = Number(($('#depth') as HTMLSelectElement).value);
    const concurrency = Number(($('#concurrency') as HTMLInputElement).value) || 300;
    const rps = Number(($('#rps') as HTMLInputElement).value) || 0;
    const dns = ($('#dns') as HTMLInputElement).value.trim();
    const dictPath = ($('#dict') as HTMLInputElement).value.trim();

    liveResults = [];
    viewRows = [];
    viewMode = 'live';
    recheckSel.clear();
    activeHistoryId = null;
    diffPair = null;
    ($('#tbody') as HTMLTableSectionElement).innerHTML = '';
    ($('#detail-body') as HTMLElement).innerHTML = '<span class="empty">点击左侧行查看详情</span>';
    $('#count').textContent = '0';
    updateModeLabel();
    renderHistoryList();
    const bar = $('#bar') as HTMLElement;
    bar.classList.remove('active');
    bar.style.width = '0%';
    setProgress(null);
    setActive(true);
    setStatus(`正在爆破 ${target} …`);

    try {
        await StartScan(target, custom, depth, concurrency, rps, dns, dictPath);
        dbg('StartScan 调用成功返回（未报错）');
    } catch (err) {
        setActive(false);
        setStatus(`启动失败：${String(err)}`);
        dbg(`StartScan 报错: ${String(err)}`);
    }
}

async function stopScan(): Promise<void> {
    try {
        await StopScan();
    } catch (err) {
        setStatus(`停止失败：${String(err)}`);
    }
}

// ---------- 事件订阅 ----------

function bindEvents(): void {
    EventsOn('scan:started', () => { dbg('事件 scan:started'); setStatus('扫描已启动'); });
    EventsOn('scan:progress', (p: Progress) => {
        setProgress(p);
        ($('#bar') as HTMLElement).classList.toggle('active', p.active);
    });
    EventsOn('scan:results', (batch: Result[]) => { appendResults(batch || []); });
    EventsOn('scan:done', (stats: Stats) => {
        dbg('事件 scan:done');
        setActive(false);
        const bar = $('#bar') as HTMLElement;
        bar.classList.remove('active');
        bar.style.width = '100%';
        setProgress({queried: stats.queried, hits: stats.hits, wildcards: stats.wildcards, unreviewed: stats.unreviewed, active: false});
        const ms = stats.duration_ms;
        const sec = (ms / 1000).toFixed(1);
        const why = stats.error ? ` · ${stats.error}` : '';
        setStatus(`完成：${stats.queried} 查询，${sec}s，命中 ${stats.hits}${why}`);
        void refreshHistory();
    });
    EventsOn('history:changed', () => void refreshHistory());
}

// ---------- 工具 ----------

function linkSlider(rangeId: string, numId: string, def: number): void {
    const r = $(rangeId) as HTMLInputElement;
    const n = $(numId) as HTMLInputElement;
    r.addEventListener('input', () => {
        n.value = r.value;
    });
    n.addEventListener('change', () => {
        const v = Math.min(Math.max(Number(n.value) || 0, Number(r.min)), Number(r.max));
        r.value = String(v);
        n.value = String(v);
    });
    r.value = String(def);
    n.value = String(def);
}

// ---------- 入口 ----------

function main(): void {
    renderLayout();

    const stored = store.get(THEME_KEY) || DARK;
    applyTheme(stored === LIGHT ? LIGHT : DARK);
    void updateMaxIcon();

    // 顶栏窗口控制
    document.getElementById('theme-toggle')!.addEventListener('click', () => {
        applyTheme(currentTheme() === DARK ? LIGHT : DARK);
    });
    document.getElementById('btn-min')!.addEventListener('click', WindowMinimise);
    document.getElementById('btn-max')!.addEventListener('click', () => {
        void WindowToggleMaximise();
        setTimeout(() => void updateMaxIcon(), 80);
    });
    document.getElementById('btn-close')!.addEventListener('click', Quit);
    const topbar = document.querySelector('.topbar') as HTMLElement;
    topbar.addEventListener('dblclick', (e) => {
        if ((e.target as HTMLElement).closest('.controls')) return;
        void WindowToggleMaximise();
        setTimeout(() => void updateMaxIcon(), 80);
    });
    window.addEventListener('resize', () => void updateMaxIcon());

    // 右键菜单：点击空白处或右键别处关闭
    document.addEventListener('click', hideCtxMenu);
    document.addEventListener('contextmenu', (e) => {
        if (!(e.target as HTMLElement).closest('.ctx-menu')) hideCtxMenu();
    });

    // 并发 / 限速滑块联动
    linkSlider('concurrency-range', 'concurrency', 300);
    linkSlider('rps-range', 'rps', 0);

    // 扫描控制
    document.getElementById('btn-start')!.addEventListener('click', () => {
        startScan().catch((e) => dbg(`startScan 异常: ${e}`));
    });
    document.getElementById('btn-stop')!.addEventListener('click', () => void stopScan());

    // DNS 预设 chip 选中态
    const chips = Array.from(document.querySelectorAll<HTMLButtonElement>('#dns-presets .chip'));
    chips.forEach((chip) => {
        chip.addEventListener('click', () => {
            chips.forEach((c) => c.classList.remove('active'));
            chip.classList.add('active');
            ($('#dns') as HTMLInputElement).value = chip.dataset.dns || '';
        });
    });
    chips.find((c) => c.dataset.dns === DEFAULT_DNS)?.classList.add('active');

    // 筛选
    document.getElementById('filter')!.addEventListener('change', (e) => {
        currentFilter = (e.target as HTMLSelectElement).value as typeof currentFilter;
        renderTable();
    });

    // 导出
    document.getElementById('btn-export')!.addEventListener('click', exportCSV);

    // 批量复核 + 全选
    document.getElementById('btn-recheck')!.addEventListener('click', () => void recheckBatch());
    document.getElementById('btn-del-checked')!.addEventListener('click', () => void batchDelete());
    document.getElementById('chk-all')!.addEventListener('change', (e) => {
        onSelectAll((e.target as HTMLInputElement).checked);
    });

    // 自定义确认弹窗
    bindConfirm();

    // 帮助面板
    document.getElementById('btn-help')!.addEventListener('click', () => {
        $('#help-ovl').classList.add('show');
    });
    document.getElementById('help-close')!.addEventListener('click', () => {
        $('#help-ovl').classList.remove('show');
    });
    document.getElementById('help-ovl')!.addEventListener('click', (e) => {
        if (e.target === e.currentTarget) $('#help-ovl').classList.remove('show');
    });

    // 历史面板
    document.getElementById('btn-refresh')!.addEventListener('click', () => void refreshHistory());
    document.getElementById('btn-live')!.addEventListener('click', switchToLive);
    document.getElementById('btn-diff')!.addEventListener('click', () => void runDiff());
    updateDiffUI();

    // 字典选择 + 词数提示
    function updateDictNote(dictPath: string): void {
        void GetDictWords(dictPath).then((words) => {
            const note = document.getElementById('dict-note');
            if (note) note.textContent = `字典 ${words.length} 词`;
        });
    }
    updateDictNote('');

    document.getElementById('btn-browse')!.addEventListener('click', async () => {
        const p = await PickDict();
        if (p) {
            ($('#dict') as HTMLInputElement).value = p;
            updateDictNote(p);
        }
    });

    bindEvents();
    void refreshHistory();
}

async function updateMaxIcon(): Promise<void> {
    const btn = document.getElementById('btn-max');
    if (!btn) return;
    const maximised = await WindowIsMaximised();
    btn.innerHTML = maximised ? ICON_RESTORE : ICON_MAX;
    btn.title = maximised ? '还原' : '最大化';
}

main();
