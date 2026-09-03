# Task 4 阶段 4：实现与验证报告

日期：2026-09-03。状态：实现完成，前端验证通过；Windows 缺少 Go/gofmt，后端编译、测试、格式化及真实 Docker E2E 待 Ubuntu 验证。未 commit，未 push。

## 1. Wiki edge 实际语义

WikiPage 的 OutLinks 是解析正文 Wiki 超链接得到的目标 slug；InLinks 是反向引用。WikiGraphEdge 只有 source/target，没有 prerequisite/relation type。图节点稳定身份是 WikiPage.id，边端点是当前 KB 内 slug。推荐邻接遍历把真实有向边视为双向可达，但返回的 context_graph 保留原始方向。理由仅为“直接相连/两跳相关”，不是先修知识。

现有 overview 按 link_count 排序并限制输出；ego 对入边/出边做 BFS。现有 GetGraph 底层仍 ListAll 全行读取，因此新推荐读取采用同一 Wiki repository 的有界、无正文投影，而不重复调用多个 ego 或读取全库正文。

## 2. LearningGapCandidate 定义

内部类型 learningGapCandidate：当前 KB、同租户、非归档且非 index 的 WikiPage，缺少有效已点亮状态，通过一条或两条真实 Wiki edge 获得 anchor 支持。unknown 不表示一定不知道，候选也不表示确定的知识盲区。exposed/familiar/mastered 不进入 unknown 推荐列表。

## 3. Anchor 定义

当前 tenant + subject 的 UserKnowledgeState，位于读取的图中，evidence_count > 0，状态为 mastered/familiar/exposed。权重分别为 1.00/0.80/0.30；unknown 不作 anchor。confidence 是证据可靠性，不是掌握概率；本次不用于推荐评分。

## 4–5. 推荐公式、分项和权重

`score = clamp(0.65*S + 0.20*A + 0.05*M + 0.05*R + 0.05*L, 0, 1)`

| 分项 | 定义 | 权重 |
| --- | --- | --- |
| structural | 一跳 1，二跳 0.45 | 0.65 |
| anchor_strength | 最强 supporting anchor 的状态权重 | 0.20 |
| multi_anchor | min(1, (不同 supporting anchors 数 - 1) / 2) | 0.05 |
| recency | supporting anchors 的 max(exp(-max(days, 0)/30)) | 0.05 |
| long_term_memory | supporting anchors 中是否存在 memory_link/familiarity 支持 | 0.05 |

权重总和为 1，集中定义在 learning_recommendation_rank.go。每个分项和最终值都限制到合法范围。最弱一跳至少 .71，最强二跳至多 .6425，因此近期性和记忆 bonus 不会反转距离优先级。

排序：score DESC、hop ASC、title ASC、wiki_page_id ASC。节点、邻接列表、支持节点和理由码都稳定排序，不依赖 map 迭代顺序。纯函数接受固定时间；API 的 scoring_at 按 UTC 小时取整，同一小时相同数据排序一致，跨小时允许 recency 自然变化。

## 6. 不使用 LLM

候选来自实际 Wiki 边，评分仅依赖状态、时间和明确的 Evidence 类型。没有 LLM、embedding、标题语义猜测，也不使用 retrieval mapping score 决定推荐。

## 7. 不持久化 Recommendation

每次 GET 从当前数据计算，未新增数据库表或 migration。Recommendation 是派生视图，不是 KnowledgeState。新 exposure 到达后，下次 GET 立即按最新状态重新计算，不保留推荐状态行。

## 8–9. 跳数、遍历和高 degree 限制

先完成一跳候选及其全部支持收集；一跳数量不足 limit 时才扩展二跳。只收集每个候选最短 hop 层级的支持；同一 anchor 的重复路径只算一次。防 self-loop、cycle、重复边和重复支持。

图投影最多 1000 个节点（按稳定 ID 顺序取切片），最多 20000 条有向边，邻接访问最多 100000 次，API 最多返回 20 条。超过边/访问/节点限制会设置 truncated。index 页不参与，避免目录页把所有内容连接成无意义推荐。多 anchor bonus 在三个 anchor 时封顶，强度用 max，不随 degree 无界累加。

## 10–11. 长期 Memory 与 Recency

批量查找当前 scope、当前图节点内，evidence_type=memory_link、level=familiarity、source_type=memory_wiki_link 的 distinct WikiPage IDs。该信号表示长期记忆已经关联的知识区域，不等于特定学习目标。

现有 memory metadata 有 memory_item_id、knowledge_base_id、mapping_score、mapping_method；chat metadata 另有 message_id、session_id、rank。metadata 不包含 MemoryItem.kind，不能可靠区分 goal/interest。现有 Memory kind 为 profile/preference/fact/task/interest，本次不重新分析内容、不重新提取 goal。

Recency 使用 anchor.last_evidence_at，缺失为 0，未来时间钳制为 1，旧数据指数趋近 0，时区不改变时间差；权重仅 0.05。

## 12. Scope 与批量读取

ResolveScope 从 authenticated context 取得 tenant 和 Principal.StorageID。KB 归属必须匹配，不把共享 KB 资源租户当 profile 租户。Handler 不绑定 tenant_id/user_id/subject_id 查询参数。

通常四次有界/批量查询：KB、Wiki 图投影、图内 scoped states、scoped memory-support IDs。没有每候选数据库查询。所有 profile 查询都有 tenant_id + subject_id，页面范围同时约束在图节点 ID 集合内。

## 13. 新增文件

后端：

- internal/types/learning_recommendation.go
- internal/types/interfaces/learning_recommendation.go
- internal/application/repository/learning_recommendation.go
- internal/application/repository/learning_recommendation_test.go
- internal/application/service/memory/learning_recommendation.go
- internal/application/service/memory/learning_recommendation_rank.go
- internal/application/service/memory/learning_recommendation_test.go
- internal/handler/learning_recommendation.go
- internal/handler/learning_recommendation_test.go

前端：

- frontend/src/views/knowledge/wiki/learningRecommendations.ts
- frontend/src/views/knowledge/wiki/learningRecommendations.test.ts
- frontend/src/views/knowledge/wiki/LearningRecommendationPanel.vue
- frontend/src/views/knowledge/wiki/LearningRecommendationDetails.vue

文档：本文件。

## 14. 修改文件

- internal/types/interfaces/wiki_page.go：增加有界轻量图投影方法。
- internal/types/interfaces/learning_profile.go：增加批量 recommendation signals 读取方法。
- internal/handler/memory.go：注入推荐 service。
- internal/router/routes_memory.go：注册 GET 路由，沿用现有认证/RBAC。
- internal/container/container.go：注册推荐 service。
- internal/handler/learning_profile_test.go、memory_wiki_test.go：仅补 constructor 新参数 nil，不改变旧断言。
- frontend/src/api/memory.ts：推荐 DTO/API。
- frontend/src/views/knowledge/wiki/personalLearningGraph.ts：按 page ID 叠加推荐及推荐筛选。
- frontend/src/views/knowledge/wiki/WikiBrowser.vue：卡片、overlay、Drawer、刷新与过期响应防护。
- frontend/src/i18n/locales/{zh-CN,en-US,ko-KR,ru-RU}.ts：新增同构翻译键。

## 15. API

`GET /api/v1/memory/learning-recommendations?knowledge_base_id=<required>&limit=5`

limit 默认 5，显式值必须 1–20；空值、重复值、非法整数或越界值返回 400。跨租户/不存在 KB 使用现有 404 语义；无 authenticated scope 为 401。

返回 success/data；data 包括 knowledge_base_id、generated_at、scoring_at、wiki_enabled、truncated、recommendations、context_graph。每项有 ID/title/slug/status=unknown/score/rank/hop/reason_codes/supporting_nodes/score_components。支持节点含 evidence_count、last_evidence_at、memory_supported 和真实路径 ID。

无 Wiki：200、wiki_enabled=false、recommendations=[]。无 anchor 或无候选：200、wiki_enabled=true、recommendations=[]。context_graph 只是现有 Wiki 的推荐支撑切片，不是第二套图谱。

## 16–19. 前端入口、Overlay、Drawer、Debug

入口：现有 WikiBrowser 的“个人画像”，画像统计下方增加“下一步学习建议”卡片。点击卡片复用 handleGraphSearchSelect：定位、选择节点、打开原 Drawer；无需新页面。

Overlay 按 wiki_page_id + KB 身份合并，与 knowledge_state 分离。unknown 原色不变，使用 TDesign warning token 的虚线外圈和 #rank，与 mastered 的星形区分，支持主题切换。已变为 exposed 的节点即使收到旧推荐响应，也不再附加推荐标记。

保留原有筛选，新增“只看推荐”，同时保留 supporting anchor/真实路径；该模式优先于“只看已点亮”。补充 server context_graph，以便 overview 之外的推荐也能展示支持关系。

Drawer 新增理由、推荐度、排名、支持节点及状态。正常视图不展示分项。Debug 开启后只展示 allow-list：score、rank、hop、五分项、支持 IDs、路径、KB ID、generated_at、scoring_at；不输出请求对象、任意 metadata、prompt 或凭证。

推荐请求失败不使图失败；清除旧推荐并退出仅推荐筛选，可重试。刷新/重新进入画像及原有窗口焦点刷新会重新计算推荐。迟到的旧请求不能覆盖新响应。

## 20. 后端测试

新增 12 个 TestLearningRecommendation* 函数（含表驱动子场景）：

- CandidatesAndRanking
- MultipleAnchorsAndCappedDegree
- TwoHopFallbackAndCycles
- RecencyMemoryAndScoreSafety
- StableRankingAndTraversalBound
- IncomingWikiLinksAreAdjacencyNotPrerequisites
- ServiceScopeAndEmptyResults
- ClosedLoopAndMappingScoreIgnored
- RepositoryBatchScope
- HandlerSuccessAndForgedScopeIgnored
- HandlerInvalidRequests
- HandlerEmptyAndCrossTenant

覆盖强弱 anchor、known 排除、KB/tenant/user 隔离、缺少状态/无 Wiki、时间边界、score 范围、稳定排序、重复边/self-loop/cycle、访问/节点/边上限、JSON、伪造 scope 和空数组。

闭环使用原有 SQLite service harness 和真实 LearningProfile repository：unknown 被推荐 → RecordChatInteractions 写 exposure → 重新推荐不再包含该节点。另验证修改 memory mapping_score 不改变 recommendation score。这里只编写了测试，尚未执行 Go 测试，不能视为运行证据。

## 21. 前端测试

新增 10 项 Node/tsx 测试，复用现有体系和 Vue 已有 compiler/SSR 依赖，不增加 package dependency。覆盖 ID merge、不改变状态、排名、导航调用、支持路径筛选、i18n、Debug allow-list、失败隔离、请求竞态、unknown→exposed。

实际编译并 SSR 渲染新 Vue 组件，验证 Drawer 理由/支持节点、Debug on/off、卡片排名、空/无 Wiki/失败状态。点击到真实浏览器 SVG/Drawer 的完整交互仍属于 Docker E2E 待验，SSR 不代替浏览器验证。

## 22–24. 实际命令与结果

命令执行目录：前端为 frontend，后端为仓库根目录。FAIL（环境）表示命令已尝试但进程未能启动，不表示测试通过或断言失败。

| 命令 | 实际结果 |
| --- | --- |
| `npm test -- src/views/knowledge/wiki/learningRecommendations.test.ts src/views/knowledge/wiki/personalLearningGraph.test.ts` | 首次 FAIL：新测试 runtime alias 无法解析；改相对导入后 PASS 13/13，最终重跑同样 PASS |
| `npm run check-i18n` | PASS 11/11 |
| `npm test` | PASS 546/546，无 skipped |
| `npm run type-check` | 初次 PASS；加入 SSR 测试后 FAIL：测试替身的 $slots 类型；改用 defineComponent 后 PASS |
| `npm run build-only` | PASS，6437 modules，1m46s；存在 >500 kB chunk 警告 |
| 下列完整 `gofmt -w` 命令 | FAIL（环境）：找不到 gofmt，未格式化 |
| `go test ./internal/application/service/memory -run 'LearningRecommendation\|LearningProfile\|ChatLearning\|MemoryWiki' -count=1 -v` | FAIL（环境）：找不到 go，未执行 |
| `go test ./internal/application/repository -run 'LearningRecommendation\|LearningProfile\|MemoryWiki\|WikiPage' -count=1 -v` | FAIL（环境）：找不到 go，未执行 |
| `go test ./internal/handler -run 'LearningRecommendation\|LearningProfile\|MemoryWiki' -count=1 -v` | FAIL（环境）：找不到 go，未执行 |
| `go test ./internal/handler/session -run ChatLearning -count=1 -v` | FAIL（环境）：找不到 go，未执行 |
| `go test ./internal/router ./internal/types ./internal/container -count=1` | FAIL（环境）：找不到 go，未执行 |
| `git -c safe.directory=D:/WeKnora/WeKnora diff --check` | PASS，静态空白检查而非编译 |

完整格式化命令（已尝试，需 Ubuntu 重跑）：

```bash
gofmt -w internal/types/learning_recommendation.go internal/types/interfaces/learning_recommendation.go internal/types/interfaces/learning_profile.go internal/types/interfaces/wiki_page.go internal/application/repository/learning_recommendation.go internal/application/repository/learning_recommendation_test.go internal/application/service/memory/learning_recommendation.go internal/application/service/memory/learning_recommendation_rank.go internal/application/service/memory/learning_recommendation_test.go internal/handler/learning_recommendation.go internal/handler/learning_recommendation_test.go internal/handler/memory.go internal/handler/learning_profile_test.go internal/handler/memory_wiki_test.go internal/router/routes_memory.go internal/container/container.go
```

当前 package.json 没有 formatter/lint script，也没有配置对应工具；未安装新工具或冒称 lint/formatter 通过。

## 25. Ubuntu / Docker 必须补验

1. 运行上面的 gofmt 和五条 Go test 命令；Go 版本按 go.mod（1.26.0），CGO/SQLite 环境沿用阶段 3。
2. 重建真实 Docker 后，打开含真实 Wiki edge 的 KB 个人画像，确认 API scope、推荐卡片、外圈、排序和 Drawer 原因一致。
3. 保留一个 unknown 候选 B：记录其 ID、rank、score、supporting nodes；在该 KB 中进行关于 B 的真实 user Chat。
4. 等待阶段 3 worker 的 evidence_written，再确认 B 的 chat_interaction/source_id 指向已持久化 user message，并且状态为 exposed。
5. 刷新画像和推荐：B 不再是 unknown 推荐、外圈消失，新 Top-N 与当前数据一致。若没有其他候选，正常显示空结果，不要求一定有替代项。
6. 使用另一个用户和租户验证推荐/证据互不串入；伪造 query scope 无效；共享资源 KB 不改变 profile tenant。
7. 浏览器验证卡片点击、视图定位、Drawer、只看推荐时的支持节点、暗色主题、API 失败和 Debug 开关。不能将本轮 SSR 测试当作这一步已完成。

## 26. 已知限制

- 大图只处理按 ID 有界截取的非归档、非 index 子图，可能遗漏切片外 anchor/邻居。明确显示 truncated，不声称全库最优。
- 二跳只补足一跳不足时的候选，不保证填满 limit；同一候选只计最短层支持，不累加更长替代路径。
- 普通 Wiki hyperlink 不是先修知识。没有测验/掌握评估、继续学习 exposed 排序、课程规划或目标推断。
- Memory support 只有明确 evidence 存在性，不区分具体 Memory kind。
- 当前 graph/state/evidence 是少量连续读取，不是一个跨表 MVCC 快照；并发 evidence 更新可在下一次刷新反映。没有 recommendation 缓存/持久化。
- 前端原画像状态 API 的既有逐页补充实现未重构；新推荐读取自身没有 N+1。
- 当前 Windows 无 Go 工具链，后端编译及 runtime 结果未知，gofmt 待执行；真实 Docker/browser E2E 也未执行。

## 27. 对阶段 1–3 的影响

未改 HybridSearch、候选 mapping/ChunkRef 规则、Chat learning payload/worker、Evidence 类型或写入语义、exposure/familiar/mastered 聚合优先级。旧 Handler 测试仅适配 constructor 参数，没有删除测试或修改旧断言。没有新数据库表、LLM 推荐或大型 graph/UI 依赖。等待人工审查，不 commit、不 push。
