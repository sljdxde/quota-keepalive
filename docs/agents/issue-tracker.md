# Issue tracker

本仓库（solo 项目、无 git remote）使用**本地 markdown 跟踪器**，主要服务 wayfinder 技能。

## 位置与文件

- 地图与全部工单在 `.scratch/wayfinder/` 下，一个 markdown 文件 = 一个 issue。
- 地图：一个努力一个地图，文件名 `map-<effort>.md`（frontmatter `labels: [wayfinder:map]`）。旧努力的地图保持原位；当前独立重构使用 `map-standalone.md`，已关闭的设计地图仍为 `map.md`。
- 工单：`NNN-slug.md`，frontmatter 含：
  - `id`：`qka-NNN`
  - `title`：工单名（叙述中一律用名字，不用裸 id）
  - `labels`：`wayfinder:research | wayfinder:prototype | wayfinder:grilling | wayfinder:task` 之一
  - `status`：`open | closed`
  - `assignee`：认领后填驱动者；空 = 未认领
  - `blocked-by`：阻塞本工单的工单 id 数组（本地 markdown 无原生依赖关系，以此表达阻塞）
- 研究产出文件：`.scratch/wayfinder/research/<slug>.md`。

## Wayfinding operations

- **认领**：开工前先把工单 `assignee` 设为驱动者，先于任何工作；并发 session 靠它避让。
- **frontier**：`status: open` 且 `assignee` 为空且 `blocked-by` 全部已 `closed` 的工单。
- **关闭**：工单正文追加 `## Resolution` 记录结论，`status: closed`，并在地图 `## Decisions so far` 追加一行（工单名链接 + 一行结论 gist）。
- **范围外**：发现工单超出目的地时直接关闭，在地图 `## Out of scope` 记一行（不进 Decisions so far）。
- **迷雾 graduate**：地图 `## Not yet specified` 里的模糊问题一旦能精确表述，就建新工单（先建文件拿到 id，再在依赖方补 `blocked-by`）。
- 每个 session 最多解决一张工单（research 工单除外，可并行派子代理）。
