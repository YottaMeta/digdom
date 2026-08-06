## 简介

DigDom v1.0.0 首个发布版：一款快速、易用的主动子域名挖掘工具，提供桌面 GUI 与命令行两种形态，历史数据互通。

## 新增

- 高并发 DNS 爆破引擎，支持多级递归、通配符过滤与速率控制
- HTTP 探活批量复核、历史记录、两次扫描 diff 对比
- 命令行版 `digdom-cli.exe`，与 GUI 共用历史库

## 使用说明

- 解压后运行 `digdom.exe` 启动桌面端
- 命令行版：`digdom-cli.exe -h` 查看子命令
- 首次运行自动生成历史库 `digdom.db`
- 内置 `dic.txt`（175 万词字典）；删除后仍可用内置默认字典运行

## 备注

- 本包为 Windows amd64 版本，由本机构建发布
- 源码已开源：`https://github.com/YottaMeta/digdom`
