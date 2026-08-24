# 数据库演进

TokenHub 使用显式、只前进的迁移演进数据库。本文说明演进模型、维护命令，以及升级与回退和数据库的关系。

本文是仓库内数据库演进生命周期与安全契约的规范来源；`backend/internal/dbschema`、维护 CLI、托管升级和 CI 均以此为准。

## 模型

- **采纳基线**：每个数据库携带迁移 ledger（`schema_migrations`）。旧版本创建的数据库会在下一次启动时被采纳：冻结的结构流程将其补齐，与参考快照做语义校验，然后记录基线。全新数据库直接由冻结的基线 SQL 创建，不再走 ORM 流程。
- **扩展迁移**添加兼容结构并在启动时自动执行；**收缩迁移**移除旧结构，绝不在启动时执行，只能通过满足前置条件的维护命令执行。
- **校验和与 dirty 状态**：每次启动都会校验已应用迁移的校验和。非事务迁移失败会留下 dirty 标记并拒绝启动，直到完成修复；事务迁移失败只回滚自身版本。
- **数据回填**记录在独立的 ledger（`data_backfills`）中。阻塞式回填必须完成后实例才就绪；在线回填以幂等分批执行，服务不中断，并通过 lease 在集群内协调为单一逻辑任务，执行者失效后由其他实例接管。
- **实例心跳**：每个运行中的实例以 TTL 发布自身版本。存在未过期心跳的实例时，收缩维护拒绝执行。
- **回退兼容性**：每个 Release 声明自己能完整运行的数据库状态范围。托管回退先执行只读预检：没有受验证兼容记录的 Release 以 `unknown` 拒绝；数据库状态超出目标范围以 `incompatible` 拒绝。管理界面将回退目标标注为数据库兼容、不兼容或未知。

`/readyz` 与 `/healthz` 在 ledger 处于 dirty、无法校验或阻塞式回填未完成时失败；在线回填挂起不影响就绪。

## 维护命令

主二进制提供 `db` 子命令：

```bash
tokenhub db status                                  # ledger、回填、在线实例
tokenhub db verify                                  # ledger 校验和 + 语义校验
tokenhub db prepare                                 # 以启动兼容方式采纳并执行扩展迁移，但不提供服务
tokenhub db migrate                                 # 执行待执行的扩展迁移
tokenhub db repair --version <n>                    # 清除 dirty 迁移（仅限受验证的修复）
tokenhub db contract --dry-run                      # 收缩迁移预检
tokenhub db contract --backup-reference <ref> --maintenance
```

数据库连接取自 `TOKENHUB_DATABASE_URL`（或默认 SQLite 路径）。

`contract` 在执行任何操作前要求：全部数据回填完成、不存在未过期的实例心跳、操作者提供已验证的备份引用、显式确认满足维护条件。SQLite 请先创建内置备份；PostgreSQL 请提供你自行验证过的外部备份引用。

## 运维要点

- 对没有采纳基线的数据库执行 `tokenhub db migrate` 会提示先正常启动一次服务：采纳在服务的串行结构流程中完成。
- 被拒绝的 contract 会说明失败的前置条件；此时没有执行任何操作。
- 回退到旧版本后，旧版本可在当前数据库上继续工作；新版本回归时重新校验 ledger 并继续演进。
- 托管升级会先用目标 Release 自身的二进制执行 `db prepare`：在不发布服务实例心跳的情况下，运行串行且与启动一致的采纳与 expand 流程，因此受支持但尚无 ledger 的旧数据库也能在激活前完成准备；随后执行 `db verify`，对准备后的 ledger 和数据库结构做语义校验。两者都成功后才激活目标 Release。激活后的 Release 首次启动失败时，自动重新激活上一 Release 一次（仅当本次升级未执行 contract、且上一 Release 的兼容记录覆盖当前数据库状态时）；第二次失败则停止版本切换，交由人工恢复。
- 管理界面展示只读的数据库演进区块（状态版本、就绪状态、兼容范围、回填、在线实例）；contract 与 repair 操作按设计只存在于 CLI。

## 开发者说明

- 迁移运行器位于 `backend/internal/dbschema`；冻结基线 SQL 按方言嵌入在 `backend/internal/dbschema/migrations/` 下。
- 模型变更后重新生成 SQLite 基线：`UPDATE_BASELINE=1 go test ./internal/server -run TestSQLiteBaselineSQLIsCurrent`；PostgreSQL 基线以同样方式生成，需设置 `TEST_POSTGRES_URL` 并加 `integration` 构建标签。基线过期时测试会失败。
- CI 会运行 PostgreSQL 集成测试，并在 SQLite 和 PostgreSQL 上执行 v0.4.0 N-1 双向契约：旧版本创建数据库，当前版本采纳并进入就绪，旧版本再次启动并完成 API 契约（认证、项目、API key、Provider、Model 与 Route、一次网关请求和审计写入），随后当前版本返回并读取部分持久化记录（两个方言都检查项目和模型，SQLite 还检查 Provider）。另一个 SQLite 流程会固定真实 v0.5.0 的数据库结构、完成采纳，并证明两个版本都能启动；该流程不运行 CRUD 契约，也没有 v0.5.0 PostgreSQL 流程。`backend/internal/dbschema/fixtures/` 下已提交的不可变 fixture 覆盖两个方言的 v0.4.0 和 SQLite 的 v0.5.0；CI 会在采纳前用 `go run ./cmd/n1check` 校验对应数据库。
- 注册表或基线变更后重新生成内嵌迁移 manifest：在 `backend/` 下执行 `go run ./cmd/manifestgen`；内嵌副本过期时 CI 会失败。
