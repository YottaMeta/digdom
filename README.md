# 域探 · DigDom 子域名爆破

一款快速、易用的主动子域名挖掘工具。内置高并发 DNS 爆破引擎，支持多级递归、历史记录、结果复核与差异对比，提供桌面 GUI 与命令行两种形态，历史数据互通。

## 功能特性

- **主动子域名爆破**：多级递归（0/1/2），通配符过滤（泛解析识别）
- **高并发 + 限速**：并发数与每秒查询数（RPS）可分别调节，兼顾速度与对目标 DNS 的友好度
- **HTTP 探活复核**：内置 HTTP/HTTPS 探活，一键批量确认域名是否可达
- **历史记录**：每次扫描自动落库（SQLite），随时回看
- **差异对比**：任意两次历史扫描 diff，新增 / 消失一目了然
- **右键菜单**：打开域名、复制 IP / CNAME / 整行、单条探活、删除
- **命令行复用**：同引擎、同字典、同存储的 `digdom-cli`，适合脚本化
- **轻量零依赖**：Go + Wails 单二进制，内置字典（583 词），无需运行时

## 快速开始

### Windows

1. 下载最新 [Release](https://github.com/你的用户名/digdom/releases) 中的 `digdom-windows-amd64.zip`
2. 解压后运行 `digdom.exe` 即可
3. （可选）把 `dic.txt` 放到 exe 同目录，程序会优先使用它作为爆破字典；不提供则使用内置字典

> 首次运行会自动在 exe 同目录生成 `digdom.db`（历史数据库）；若该目录不可写，则回退到 `%AppData%\digdom\digdom.db`。

### 从源码构建（Windows）

```powershell
# 需要 Go 1.25+、Node.js、Wails CLI（wails.io）
wails build
# 产物：build\bin\digdom.exe
```

### 命令行（CLI）

```text
digdom-cli -target example.com [-depth 0] [-concurrency 300] [-rps 0]
           [-dns 8.8.8.8,1.1.1.1] [-dict path] [-words "www,api"] [-all] [-json]
digdom-cli history [id]      # 列出历史 / 查看某次结果
digdom-cli diff <a> <b>      # 对比两次历史（a=基准旧，b=当前新）
```

CLI 与 GUI 共用同一份历史数据库，扫描结果相互可见。

## 使用说明

### 爆破参数

| 参数 | 说明 |
| --- | --- |
| 目标 | 要爆破的主域名，如 `example.com`（不带 http/www） |
| 递归深度 | 是否继续爆破命中的子域名（0 只爆第一层，越大越耗时） |
| 并发 | 同时发起的 DNS 查询数，越大越快也越耗资源 |
| 限速/秒 | 每秒最多发起的查询数（0 = 不限），控制对目标 DNS 的冲击 |
| DNS 服务器 | 查询用 DNS，右侧预设一键填常用公共 DNS |
| 字典 | 爆破词表；「浏览」选文件，或「自定义追加词」临时加词（自动合并去重） |

> 关于并发与限速：**并发**决定同时在途的查询数（峰值占用）；**限速**决定每秒新发起的查询数（平均节奏）。实际吞吐受两者共同约束——并发高而限速低时以限速为准。

### 结果列表

- **标签**：`hit`=命中（有解析）、`wildcard`=通配符（泛解析干扰，多为假命中）、`unreviewed`=待处理
- **探测**：HTTP 探活结果（状态码 / 不可达 / 未探活），由「批量复核」写入
- **筛选**：按标签过滤；表头勾选框全选可见行
- **右键行**：打开域名 / 复制域名 / IP / CNAME / 整行；历史模式另可「探活该条」「删除该条」

### 历史 / 复核 / 对比

- 点开左侧历史查看当时结果；勾选两条后点「对比所选」用 diff 对比（新增绿 / 消失红）
- 右侧详情栏可对单条标「确认真实存在 / 确认误报」并加备注
- 「批量复核」对勾选行（或全部）做 HTTP 探活：可达自动标确认，不可达仅记录
- 「批量删除」删除当前勾选的历史结果（有确认，不可恢复）

## 数据存储

- **数据库**：`digdom.db`，优先存 exe 同目录（便携），不可写时回退 `%AppData%\digdom\`
- 首次运行自动建库建表，无需手动初始化
- 表结构：`scans`（扫描记录）、`results`（结果明细，含标签 / 复核结论 / HTTP 探测字段）

## 打包 Release（本地发布）

本项目**不使用 CI 自动构建**，发布流程全部在本机完成：构建 → 打包（含字典）→ 打 tag → gh 上传。

完整可执行步骤见 [docs/GitHub发包流程与规范.md](../docs/GitHub发包流程与规范.md)（已为 AI 智能体按步骤编写）。速览：

```powershell
# 1. 改版本号（main.go 的 buildVersion）→ 验证（go test / tsc）
# 2. 构建
wails build
go build -o build\bin\digdom-cli.exe ./cmd/digdom-cli

# 3. 打包（字典必带）
New-Item -ItemType Directory -Path release -Force | Out-Null
Copy-Item build\bin\digdom.exe, build\bin\digdom-cli.exe release\
Copy-Item internal\engine\dict\dic.txt release\
Compress-Archive -Path "release\*" -DestinationPath "release\digdom-v1.0.0-windows-amd64.zip" -Force

# 4. 打 tag + 发 Release
git tag v1.0.0
git push origin v1.0.0
gh release create v1.0.0 "release\digdom-v1.0.0-windows-amd64.zip" --title "DigDom v1.0.0" --notes-file docs\release-notes.md
```

跨平台需在对应系统构建：macOS `wails build -platform darwin/universal2`、Linux `wails build -platform linux/amd64`。

### Release 压缩包内容

```text
digdom-<version>-<os>-<arch>.zip
├── digdom.exe / digdom / digdom.app
├── digdom-cli.exe / digdom-cli   （命令行版）
└── dic.txt                       （175 万词字典，必带）
```

> Release 包**必带** `dic.txt`（175 万词大字典），解压后 exe 自动优先加载它；即使删除字典文件，程序仍可用内置字典（583 词）运行。

## 目录结构

```text
digdom/
├── main.go               # 程序入口（Wails 窗口）
├── app.go                # Wails 绑定（扫描 / 历史 / 复核 / diff）
├── cmd/digdom-cli/       # 命令行工具
├── internal/
│   ├── brute/            # DNS 查询 / 并发控制
│   ├── engine/           # 爆破引擎 + 内置字典
│   ├── httpcheck/        # HTTP 探活（批量复核）
│   ├── store/            # SQLite 存储
│   └── model/            # 数据模型
└── frontend/             # 前端界面（TS + 原生 HTML/CSS）
```

## License

[MIT](LICENSE)

## 免责声明

本工具仅用于**授权范围内**的域名资产发现与安全评估。请勿对未授权目标发起爆破。使用者需自行承担一切法律责任。
