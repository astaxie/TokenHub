# 使用只前进的数据库演进并分离版本回退与数据库恢复

TokenHub 将以 bridge release 为现有数据库建立迁移基线和兼容检查，而不反推完整历史迁移链；此后的数据库变更使用显式、只前进的版本化迁移。应用启动只自动执行仍处于受支持回退窗口内的 expand migration，破坏旧版本兼容性的 contract migration 必须通过显式维护操作执行。

应用版本回退只切换 Release 并保留当前数据库，不执行 down migration 或自动恢复数据库；目标 Release 无法安全读写当前数据库状态时必须拒绝回退。数据库恢复保留为独立的灾难恢复流程，因为自动恢复快照可能丢失升级后写入的数据。

受支持回退窗口至少包含紧邻的上一个稳定版本；更早 Release 只有在兼容清单明确允许时才开放。Schema 变化以分别冻结的 SQLite 与 PostgreSQL SQL 为权威来源，只有需要业务算法的数据回填使用编号 Go migration；数据回填默认阻塞就绪，只有具备兼容读写、幂等续跑和进度记录时才能声明为在线执行。

Bridge release 遇到没有迁移记录的遗留数据库时，一次性运行冻结的既有修齐流程并验证关键数据库对象，全部成功后才记录 baseline；无法识别或验证的数据库拒绝启动，不能仅凭数据库非空便假定兼容。

每个 Release 必须携带数据库兼容清单，声明其目标数据库状态和完整读写兼容范围；数据库 ledger 记录实际状态，应用启动与托管版本回退都必须校验两者。事务迁移失败时回滚该版本，非事务迁移失败时保留 dirty 状态并拒绝启动，只有诊断后通过受校验的 repair 操作才能恢复，不能无限自动重试或带着未知 schema 继续运行。

Contract migration 只能由专用维护命令执行；执行前必须 dry-run，并确认相关数据回填完成、集群中没有旧版本实例、数据库已有备份，以及该迁移要求的 drain 或 maintenance 条件已经满足。在线数据回填由数据库 ledger、lease、cursor 与 heartbeat 协调为集群中的单一逻辑任务，执行者失效后由其他实例从已提交游标接管。

数据库结构迁移使用独立于应用 SemVer 的全局单调版本，Release 兼容清单声明 `target`、`min_compatible` 与 `max_compatible`，表示完整运行时读写兼容而不只是能够查询；数据回填使用独立编号。Bridge release 为紧邻的上一个稳定版本携带一条经过双向兼容测试并绑定 Release checksum 的一次性 legacy 记录；缺少兼容清单的其他旧版本不能通过普通托管回退强制激活。

数据库运维首先通过 `tokenhub db status|migrate|contract|repair` CLI 提供；Admin API 与管理界面只显示只读状态，不提供 contract 或 repair 操作。托管升级在校验目标 Release 后，先使用目标二进制完成只读预检和待执行的 expand migration，成功后才激活目标 Release 并重启；普通手工启动仍会补跑尚未完成的 expand migration。

目标 Release 激活后未通过健康检查时，只要本次升级没有执行 contract，且上一 Release 的兼容声明覆盖当前数据库状态，系统就自动重新激活上一 Release；数据库保留已经完成的 expand，不自动恢复备份。应用版本回退发生在在线数据回填期间时，执行者停止并释放 lease，保留已提交 cursor，未来再次升级后继续执行，不撤销已经写入的兼容数据。

Contract 的备份证据按数据库类型处理：SQLite 由 TokenHub 创建并校验内置备份，PostgreSQL 由操作者提供外部备份引用和确认并写入迁移审计，应用不隐式接管 `pg_dump`、外部存储或保留策略。CI 同时维护不可变的 N-1 schema fixture，并构建实际 N-1 Release binary 执行启动与最小 CRUD；SQLite 和 PostgreSQL 都必须覆盖。

兼容判断同时检查结构迁移版本、目标 Release 必需的阻塞式数据回填、online backfill 部分完成状态的容忍声明，以及 dirty/running migration；不能只比较表结构版本。每个运行实例通过带 TTL 的数据库 heartbeat 发布 Release 版本和兼容范围，contract 发现未过期的不兼容实例时拒绝执行，不能仅依赖操作者确认或数据库 session 推断。

Fresh database 从显式的 `000001` SQLite 或 PostgreSQL SQL 创建；只有已经存在业务表但没有迁移 ledger 的数据库才运行冻结的 legacy adoption，验证后记录相同 baseline。Repair 不提供通用强制跳版本，也不允许手工篡改 ledger；每个非事务迁移必须实现专用 inspect/postcondition，只有证明目标数据库状态成立后才能清除 dirty 或安全重试。

管理端继续展示近期旧 Release，但根据数据库兼容状态标记为 `compatible`、`incompatible` 或 `unknown`；后两者禁用应用版本回退并显示具体 schema 或 backfill 原因。Ledger、结构化日志和 CLI 是启动失败时的权威状态来源；运行实例额外通过只读 Admin API/UI 和 Prometheus 暴露 current/target version、dirty migration 与 backfill progress。Blocking、dirty 或 incompatible 状态使 readiness 失败，online pending 不影响 readiness。

Expand 与对应 contract 至少跨一个稳定 Release：Release N 建立兼容结构和读写路径，Release N+1 才能在 N-1 兼容测试通过且所需 backfill 完成后携带 contract。显式维护命令不能让同一 Release 中预埋的 contract 绕过这一发布门槛。

每次托管升级最多自动回切一次，随后验证上一 Release 的健康并记录迁移与版本审计；自动回切也失败时停止进一步版本切换并进入人工恢复状态，不能在新旧 Release 之间形成重启循环。

Bridge release 的最低直接升级版本为 `v0.4.0`，legacy adoption 必须分别使用 `v0.4.0` 和 `v0.5.0` 的真实数据库 fixture 验证；更早版本或未知 fingerprint 拒绝直接接管，并提供经过受支持中间版本或数据库恢复的操作说明。只有在最低直接升级版本提高到 bridge release 或更高版本后，才能删除 legacy adoption。

N-1 兼容契约测试不能止于启动和 readiness。实际旧二进制必须在新数据库状态上完成认证、项目与团队、API key、Provider 与 Resource、Model 与 Route、一次网关请求和审计写入，随后由新二进制验证这些写入没有破坏当前不变量。
