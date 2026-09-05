# 使用窄范围的自有数据库迁移运行器

TokenHub 将在 `backend/internal/dbschema` 中实现基于 `database/sql` 的窄范围迁移运行器，直接拥有 embedded dialect SQL、Go migration callback、逐版本 checksum 与历史、dirty 状态、数据库兼容范围、PostgreSQL advisory lock 和 SQLite 事务锁。它只执行注册过的完整 SQL statement 或 Go callback，不实现通用 SQL parser，也不扩展成独立迁移框架。

`pressly/goose` 虽然支持 embedded SQL、Go migration 和逐文件事务控制，但最关键的 checksum、dirty、兼容范围及 SQLite 跨进程锁仍需 TokenHub 在外围实现；`golang-migrate` 的单一当前版本 ledger、SQL byte-stream 模型和数据库驱动差异也不适合现有 Go adoption/backfill 与逐版本审计。引入任一库都会留下两套迁移状态语义，因此选择更小但由 TokenHub 完整负责和验证的实现。

运行器不提供通用 force-version 或直接改写 ledger 的逃生口。非事务迁移通过注册的 inspect/postcondition 判断目标对象是否完整、是否可以重试或是否已经成功；fresh database 使用显式 baseline SQL，legacy `AutoMigrate` 只存在于一次性的 adoption callback 中。

每项结构迁移必须注册 version、phase、transaction mode、dialect、checksum 和必要的 postcondition；发布门禁禁止修改已发布 manifest 中的迁移内容，运行时再次校验已执行版本的 checksum。PostgreSQL advisory lock 和 SQLite transaction lock 使用可配置的有界等待，默认两分钟；等待期间报告当前执行版本，超时后保持未就绪而不无限阻塞。

Release 构建生成并嵌入 migration manifest，对 dialect SQL 和每个 Go migration 源文件计算 checksum，CI 验证生成结果与源码一致。Legacy adoption 与 `tokenhub db verify` 使用 dialect-specific 语义检查器验证表、列、类型、nullability、主键、索引和 trigger，并允许无冲突的额外对象；普通启动只校验 ledger、checksum 和廉价关键不变量，不比较容易因列顺序产生误报的原始 DDL 文本。

PostgreSQL 可以通过独立 migration DSN 使用具备 DDL 权限的凭据，未配置时为简单部署复用运行时 DSN；SQLite 始终操作同一数据库文件。每项 migration 还必须声明数据库 lock 与 statement budget：expand 使用短 lock timeout，大索引采用 concurrent/online 方式并禁止无界表重写，contract 只有在 maintenance 条件满足时才能申请更长预算。

`schema_migrations` 与 `data_backfills` 保存当前权威状态，append-only `migration_attempts` 另行记录每次执行者、应用版本、开始与结束时间、结果、耗时和稳定错误码。数据库记录不保存可能包含敏感数据的原始错误，完整错误只进入现有脱敏日志。
