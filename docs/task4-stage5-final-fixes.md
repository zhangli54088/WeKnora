# Stage 5 三项收尾修复与验证

日期：2026-09-04。基于原有未提交 Stage 5 实现增量修改；未重建导出、整画像删除或推荐功能。未 commit、未 push。

## 1. Memory 删除一致性

原问题：`Service.DeleteItem/Clear → MemoryRepository.DeleteItem/DeleteAll` 删除 MemoryItem，数据库将 MemoryWikiLink 级联删除；但 LearningEvidence.source_id 是多态来源，没有 link 外键，也没有自动重算 KnowledgeState。因此会留下 memory_link Evidence 和 familiar 状态。

实际新调用链：

```text
Service.DeleteItem / Clear
  → ResolveScope（单条删除还检查 item 归属）
  → 保留旧的 best-effort tombstone 行为
  → MemoryRepository.WithLearningCleanup
      → 开启 GORM DB transaction（Serializable）
      → scoped MemoryItem 读取/锁定
      → 删除前读取 scoped links
      → 收集、去重受影响 WikiPage ID
      → 清理 memory_wiki_link / memory_link Evidence
      → Service callback
          → recomputeKnowledgeStateWithRepo（原有聚合函数，不改算法）
          → transaction-bound MemoryRepository 删除 item/items
          → Clear 同时删除原有 topics/document affinity
      → 清理剩余 scoped links（兼容没有启用 FK 的 SQLite）
      → COMMIT；任何错误则 ROLLBACK
  → 原有 rebuildBlock
```

单条删除同时限定 tenant、subject、memory_item_id；Evidence 同时限定 tenant、subject、source_type、evidence_type、source_id 所属 scoped link 子查询。不是按 WikiPage 删除全部 Evidence。

Clear 删除本 scope 的全部 memory_link 来源 Evidence，也清理旧级联删除遗留的这类孤立 Evidence；保留 chat_message 和其他来源。页面 ID 去重后，每页重算一次。

事务真实覆盖：MemoryItem 锁定、link 查询、Evidence 查询/删除、聚合所需读取及 state upsert/delete、item 删除、link 删除；Clear 还包括 topics/document affinity 删除。Repository 向 callback 传入使用同一 `*gorm.DB` transaction 的两个 Repository。没有新增 Service 依赖或 container wiring，没有依赖环。

事务不包含既有 best-effort tombstone 写入和提交后的记忆块刷新，这是原逻辑保留，不是四表一致性之外的新承诺。若删除失败，四表回滚，但此前记录的 tombstone 可能仍存在。数据库序列化冲突也返回错误并回滚，未新增自动重试；调用者可重试。

预期状态（已编写回归测试，Go 本机未运行）：

| 删除前 | 删除相关 Memory 后 |
|---|---|
| memory-only familiar，count=1 | Evidence/State 行删除，读取为 unknown |
| memory familiarity + chat exposure，count=2 | chat 保留，exposed，count=1 |
| Memory A + Memory B familiarity | 只清 A，B 保留，familiar，count=1 |
| 一个 Memory 对多个 WikiPage | 所有受影响页面分别重算 |

整画像 DELETE 保持原有不同语义：删除全部 links/evidence/states，保留 MemoryItem、topics、公共 Wiki/KB。没有改变该实现。画像 scope 不改为共享 KB 的资源 tenant；现有聚合 Repository 的资源校验规则也没有扩大。

## 2. Export created_at

新增仅用于导出的 `types.KnowledgeStateExport`，嵌入原 `UserKnowledgeStateView` 并增加 `CreatedAt`。导出批量 JOIN 查询增加 `state.created_at`；前端组合导出类型同步增加字段。

`GET /memory/knowledge-states` 的 DTO 和返回合同未修改。旧 `/memory/export`、组合 `/memory/export?include_learning_profile=true` 均保留。metadata 白名单没有修改。

已有 Export 测试补充：created_at 非零、与数据库值相等、JSON 包含 created_at/updated_at；保留原敏感字段和白名单断言。

## 3. Debug reset

`onLearningProfileCleared()` 增加 `personalDebug.value = false`。复用已有 states/evidence/recommendation 清空及请求失效机制，没有清 graphData。

选中公共 WikiPage 可以保留；`selectedKnowledgeState` 和 `selectedRecommendation` 都是根据已清空数据计算的值。前端测试执行真实 callback 和这两个 computed 声明，断言选中页面不变、unknown、无 Evidence、无推荐详情、debug=false，错误/loading 归零，公共图保留。

## 4. 本轮增量文件（不是整个未提交 Stage 5 清单）

新增：

- `internal/application/repository/memory_learning_cleanup.go`
- `internal/application/service/memory/memory_learning_cleanup_test.go`
- `docs/task4-stage5-final-fixes.md`（本报告）

修改已有实现/测试：

- `internal/types/interfaces/memory.go`
- `internal/application/service/memory/service.go`
- `internal/application/service/memory/service_test.go`（仅扩充 SQLite fixture 的表，不改旧断言）
- `internal/types/learning_profile_data.go`
- `internal/application/repository/learning_profile_data.go`
- `internal/application/repository/learning_profile_data_test.go`
- `internal/application/service/memory/learning_profile_data_test.go`
- `internal/handler/learning_profile_data_test.go`（导出 DTO 类型同步）
- `frontend/src/api/memory.ts`
- `frontend/src/views/knowledge/wiki/WikiBrowser.vue`
- `frontend/src/views/knowledge/wiki/learningProfileActions.test.ts`

其中部分文件原已存在于工作区，但尚未被 Git 跟踪。本轮没有覆盖或删除原 Stage 5 文件。

## 5. 新增后端回归测试

均位于 `memory_learning_cleanup_test.go`：

1. `TestMemoryDeleteLearningCleanupMemoryOnly`
2. `TestMemoryDeleteLearningCleanupKeepsChat`（故意让 chat source_id 与 link ID 相同，验证 source_type 隔离）
3. `TestMemoryDeleteLearningCleanupKeepsOtherMemory`
4. `TestMemoryDeleteLearningCleanupMultiplePages`
5. `TestMemoryDeleteLearningCleanupScopeIsolation`（猜测其他用户/tenant item ID；单删、Clear 都不影响其他 scope）
6. `TestMemoryClearLearningCleanupKeepsChatAndDeduplicatesPages`
7. `TestMemoryDeleteAndClearLearningCleanupRollback`（SQLite trigger 在最终 item delete 注入失败，检查四类数据回滚；含单删与 Clear 子测试）

保留先前全部整画像删除、幂等、资源保留、推荐清空、Chat 重建及状态优先级测试。

## 6. 实际执行命令与结果

本节只列本轮验证，不借用前轮 PASS。前端在 `D:\WeKnora\WeKnora\frontend` 执行；后端在仓库根目录执行。

| 命令 | 结果 |
|---|---|
| `npm.cmd test -- src/views/knowledge/wiki/learningProfileActions.test.ts src/views/knowledge/wiki/learningRecommendations.test.ts src/views/knowledge/wiki/personalLearningGraph.test.ts` | PASS，20/20 |
| `npm.cmd test` | PASS，553/553，无跳过 |
| `npm.cmd run check-i18n` | PASS，11/11 |
| `npm.cmd run type-check` | PASS，exit 0 |
| `npm.cmd run build-only` | PASS，6441 modules，1m23s，exit 0；有大于 500 kB 的 chunk 警告；已生成 frontend/dist |
| `git -c safe.directory=D:/WeKnora/WeKnora diff --check` | PASS；存在 LF/CRLF 提示，无空白错误 |

以下命令均已尝试调用，但 PowerShell 提示 `go`/`gofmt` 不存在，进程退出 1；分类为 **NOT RUN / environment unavailable**，不是 Go 测试失败，更不是 PASS。Ubuntu 应补跑同样命令：

```bash
gofmt -w internal/types/interfaces/memory.go internal/types/interfaces/learning_profile.go internal/types/learning_profile_data.go internal/application/repository/memory_learning_cleanup.go internal/application/repository/learning_profile_data.go internal/application/repository/learning_profile_data_test.go internal/application/service/memory/service.go internal/application/service/memory/service_test.go internal/application/service/memory/memory_learning_cleanup_test.go internal/application/service/memory/learning_profile_data.go internal/application/service/memory/learning_profile_data_test.go internal/handler/learning_profile_data.go internal/handler/learning_profile_data_test.go internal/handler/learning_profile_test.go internal/handler/memory.go internal/router/routes_memory.go

go test ./internal/application/service/memory -run 'LearningProfile|MemoryWiki|Memory.*Delete|Memory.*Clear|LearningRecommendation|ChatLearning' -count=1 -v

go test ./internal/application/repository -run 'LearningProfile|MemoryWiki|Memory|LearningRecommendation' -count=1 -v

go test ./internal/handler -run 'LearningProfile|MemoryWiki|LearningRecommendation|Memory' -count=1 -v

go test ./internal/handler/session -run ChatLearning -count=1 -v

go test ./internal/router ./internal/types ./internal/container -count=1

go test ./internal/application/service/memory ./internal/application/repository -count=1
```

所以 Stage 1～4 的前端相关回归通过；后端包回归、Go 编译及 gofmt 仍待 Ubuntu。没有改无关测试期望，没有修改 package.json、版本、Dockerfile 或 Ubuntu Node 环境。

## 7. Ubuntu / Docker 最终补验

先运行上述 Go 命令；依照现有部署流程构建后端，不变更架构。前端在 Windows production build 确认成功后，将 `frontend/dist` 上传 Ubuntu，再人工执行既有流程（本轮未执行部署）：

```bash
docker compose build frontend
docker compose up -d --no-deps --force-recreate frontend
```

真实 PostgreSQL/Docker 必须补验：

- memory-only 删除 → link/evidence/state 都消失，Wiki/KB 保留。
- memory+chat 删除 → chat 原 ID/权重保留，familiar→exposed，2→1。
- 多 Memory、多页面、跨用户/tenant、旧 Memory Clear 语义。
- 删除中途失败和并发序列化冲突均不会半删。
- 组合导出 created_at/updated_at 与数据库一致，旧导出兼容、metadata 不泄密。
- 整画像 DELETE 仍保留 MemoryItem，重复 counts=0，推荐空；随后真实 Chat 重新建立 exposed。
- 打开节点 Debug/推荐详情后删除画像，Drawer 无旧技术数据，公共图保留；深浅色主题和真实浏览器下载。

## 8. 边界与结论

本轮只修复已确认的删除一致性缺陷、Export 字段和 Debug reset。未修改 ChatLearning mapping、0.7 权重、聚合优先级、RecommendationScore/候选算法、HybridSearch、Worker TenantInfo 恢复或公共图语义。

Memory 删除后的画像行为发生的是缺陷纠正：不再保留已经不存在来源的 memory Evidence；不是重新定义 Stage 1～4 的学习语义。

已知限制：后端尚未在 Go 环境编译/运行；SQLite 单测不能替代 PostgreSQL 的 FK/并发验证；tombstone 和缓存刷新保留旧 best-effort 边界；删除画像不是永久禁用学习，后续真实活动可以重建。所有结论中的运行验证范围以本报告结果表为准。等待人工审查。
