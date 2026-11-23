package mcp

// DreamPromptText defines the cross-tool 4-phase consolidation protocol modeled on Claude Code's autoDream.
const DreamPromptText = `请对当前 SMem 共享记忆库执行一次标准的四阶段 Dream 整理（对标 Claude Code autoDream），只维护记忆文件，不要改动项目业务代码：

### 阶段 1：Orient（摸底现状）
1. 调用 memory_index（include_manifest=true）查看当前所有记忆条目、更新年龄（age）、未索引文件与失效链接。
2. 评估 MEMORY.md 是否接近或超过 200 行 / 25KB 上限。

### 阶段 2：Gather（识别噪声与冲突）
找出以下需要整理的候选条目并调用 memory_read 读取全文：
- 含义重叠、分散在多个文件中的重复记忆；
- 互相矛盾的新旧结论；
- 包含模糊相对时间（如“昨天”“上周”“下周四”）的条目；
- 明显过时、或属于“代码/Git 可直接查到”的冗余条目。

### 阶段 3：Consolidate（合并、转绝对日期、只留现行真相）
- 对需要更新的条目，携带最新 etag 调用 memory_write（指定 file 与 etag）原位更新；
- 把所有相对时间改写为具体绝对日期（YYYY-MM-DD）；
- 严禁把旧值或更换经过留在正文中，只保留现行结论；
- 对已被合并或确定失效的冗余文件，携带 etag 调用 memory_delete 删除；
- feedback 与 project 类型尽量保持清晰结构（结论 + **Why:** + **How to apply:**）。

### 阶段 4：Prune & Index（精简索引）
- 确保每条记忆的 description 写成简洁明确的“触发条件”（而非空泛标题）；
- 再次调用 memory_index 确认 MEMORY.md 简洁清晰、严格控制在 200 行 / 25KB 以内；
- 最后向用户简要汇报：合并了几条、更新了几条、删除了几条，以及当前索引行数。`
