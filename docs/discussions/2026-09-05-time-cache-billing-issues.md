# 本次计费设计的关联 issue 纳入清单

核查日期：2026-09-05。代码：`076a1b2d53ba61a4d99db690533ac2a5f1e5b9c4`。GitHub 通过已认证 CLI 只读查询指定 issue 正文、评论及关联 PR；没有评论、创建、关闭 issue 或修改远程状态。关联[完整设计](2026-09-05-time-cache-billing-design.md)。

## 1. 纳入结论

| Issue | 当前状态及证据 | 本次处理方式 | 验收关联 |
| --- | --- | --- | --- |
| [#264 分时与缓存写入](https://github.com/astaxie/TokenHub/issues/264) | CLOSED；最后更新 2026-08-22；原需求包含分时、5m/1h 缓存写入和 Redis | 主线演进：补星期与全分项时段价格、版本快照、免费/缺价语义；复用已有能力 | A01–A06、A09、A21 |
| [#304 用量归属显示](https://github.com/astaxie/TokenHub/issues/304) | OPEN；最后更新 2026-09-03；报告资源 unknown、项目原始 ID；本轮未在浏览器复现 | 纳入复现与修复范围：稳定身份、可读名称、改名/删除后的历史归属与状态解释 | A07、A17、A22 |
| [#259 用户聚合额度](https://github.com/astaxie/TokenHub/issues/259) | CLOSED；最后更新 2026-08-21；已有用户跨 Key token 聚合与测试 | 回归既有聚合，并扩展持久化金额预留、共享预算及幂等，不另建用户额度体系 | A08、A10–A12、A23 |
| [#246 请求热路径大锁](https://github.com/astaxie/TokenHub/issues/246) | OPEN；最后更新 2026-08-16；涉及 PostgreSQL 进程级大锁、性能及多副本正确性 | 只纳入计费/预算的事务、预留、唯一性与恢复部分；不承诺本次完成整个大锁重构和性能项目 | A08、A11、A23 |
| [#208 缓存用量回传](https://github.com/astaxie/TokenHub/issues/208) | CLOSED；最后更新 2026-08-16；[PR #238](https://github.com/astaxie/TokenHub/pull/238) 已于 2026-08-16T13:23:48Z 合并，merge `1fed04dedec5f2556330e35451b9e7110c9dd0d1` | 已修复问题的跨协议/流式回归门禁，不宣称本次重新修复 | A20 |
| [#138 全零额度补丁发布](https://github.com/astaxie/TokenHub/issues/138) | CLOSED；最后更新 2026-08-04；本质为 v0.4.0 补丁发布请求，正文称 #97 / `cc36153` 已在 main 修复全零 limits 被视为省略；本轮未核对发布包包含关系 | 保留当前源码的清零修复，补省略/明确零/继承的字段合同和完整保存链路；不把 issue 关闭当发布验收 | A21 |
| [#221 价格编辑字段](https://github.com/astaxie/TokenHub/issues/221) | OPEN；最后更新 2026-08-16；评论在 `bf19e40` 隔离实测无法复现价格字段缺失 | 纳入当前页面、角色、模型种类下的复现和端到端保存回读；不预判功能仍缺失 | A21 |
| [#76 供应商账单接入](https://github.com/astaxie/TokenHub/issues/76) | CLOSED；最后更新 2026-08-13 | 对接已有账单入口，新增成本分项/版本证据；不重建账单连接器系统 | A18、A24 |
| [#77 对账](https://github.com/astaxie/TokenHub/issues/77) | CLOSED；最后更新 2026-08-13 | 复用对账模块，验证迟到、重复导入和有证据的差额调整 | A18、A24 |
| [#177 精确响应缓存](https://github.com/astaxie/TokenHub/issues/177) | OPEN；最后更新 2026-08-16 | 独立范围；不是供应商缓存 token 计价，不纳入本次交付 | 保持范围边界 |
| [#178 语义缓存](https://github.com/astaxie/TokenHub/issues/178) | OPEN；最后更新 2026-08-16 | 独立范围，涉及匹配、隔离、失效和命中收费语义 | 保持范围边界 |
| [#83 TPM/RPM 限制](https://github.com/astaxie/TokenHub/issues/83) | CLOSED；最后更新 2026-08-13；涉及流式预留/结算、失败释放及共享原子状态 | 作为既有 token 限流回归约束；缓存价格折扣不能自动改变 token 计量合同 | A09、A11、A23 |
| [#266 当天用量时区](https://github.com/astaxie/TokenHub/issues/266) | CLOSED；最后更新 2026-08-21；涉及仪表盘配置时区与每日切换 | 纳入 UTC 预算周期与报表所选时区的并行验证及界面解释 | A10、A25 |
| [#135 Claude Code 前缀缓存](https://github.com/astaxie/TokenHub/issues/135) | CLOSED；最后更新 2026-08-13；归因块影响前缀命中优化 | 属于缓存命中优化，不并入本次计费策略开发 | 保持范围边界 |

issue 更新时间不是部署验证时间。关闭不等于所有当前路径或新价格规则已满足；OPEN 也不等于每条报告已在当前版本复现。

#76 的正文验收框未勾选，不能据关闭状态宣布所有外部供应商账单源已完成验收。#221 的历史不可复现结论来自[协作者评论](https://github.com/astaxie/TokenHub/issues/221#issuecomment-5306835388)，只对其说明的版本和环境成立。

#221 评论提及的 Provider 模型库存删除按钮是独立问题，不因同一页面相关就扩大成计费改造。#246 要求性能前后对比（mutex profile、吞吐、p95/p99）及其他热路径正确性，不能用本次预算测试替代整个 issue 的验收。

## 2. 五项约束如何落地

**统一计量依据、按协议投影。** 每个上游尝试保存一份归一化分项与证据来源；租户交付、上游尝试和限流跨尝试累计分别解释。客户端响应只投影其应见用量，不把全部内部失败尝试加进响应。总输入 90、缓存读 80、写入 0 的样例，内部和 OpenAI 可保持总输入 90，Anthropic 普通输入应为 10；同源不等于所有同名字段相等。

**零值不再被通用默认逻辑吞掉。** 当前 `effectiveCacheReadPriceUSDPer1M` 只接受正数，零会进入 metadata 或比例估算回退（`store_backup_quota.go:555`）；这是当前缓存读取免费语义缺口。已有缓存写入 configured 标记和明确免费测试，不应推倒重写。API Key PATCH 的 `*QuotaLimits` 与 `LimitsSet` 已区分省略和传入（`admin_projects_http.go:1305`、`:1321`），应该保持；额度零与价格零含义不同。

**调用身份和上游资源分别表达。** `UsageRecord` 已有 ProjectID、APIKeyID、AttributedUserID、ProviderID、ProviderResourceID（`types.go:463`）。当前名称 helper 找不到对象会回退 ID（`frontend/features/admin/domain/entities.tsx:442`、`:650`），这可以解释报告中的一种路径，但不足以证明 #304 的实际触发原因。新增显示状态须以可信证据区分未归属、已确认删除、名称暂不可用或权限受限；前端未加载不能直接推断删除。

**复用现有并发机制，补齐金额生命周期。** PostgreSQL 的 `lockScopeForUpdate` 已使用事务 advisory lock（`store_providers.go:978`），Redis Lua 已原子处理 RPM/TPM/并发及 token 预留（`redis_billing.go:14`）。现有跨 Key token 测试通过，不能据此证明跨实例金额预留已实现。以数据库持久化金额事实为准，明确 Redis 与数据库故障恢复，不将局部原子性误写成两套系统的联合事务。

**对账接续现有系统。** 保留每次上游尝试的价格版本、时间依据、原币、汇率及分项计算。供应商账单晚到或重复到达，通过既有账单与对账入口关联记录、追加幂等调整；不将上游成本差异自动转给业务用户。

补充 #266：已经确认的 UTC 预算周期与仪表盘配置时区是两件事。报表明确实际起止时间、所用时区和费用归属依据；不能把北京时间“今日”小计强行解释成 UTC 日预算已用金额。补充 #83：缓存折扣改变价格，不改变实际 token 数，既有限流中跨失败尝试 token 累计与租户收费口径分别保留。

## 3. 本轮已执行的验证

在当前工作区仅运行已有测试，没有新增测试或修改产品代码。命令从 `backend/` 执行：

```bash
go test ./internal/server -run '^(TestAnthropicMessagesConvertsOpenAIStreamingTextAndToolCall|TestAnthropicMessagesPreservesNativeStreamingAndUsage|TestUserQuotaReservationIsAtomicAcrossAPIKeys|TestUserQuotaSettlementIsIdempotent|TestPriceUsageUsesPricingPeriodAtRequestStart)$' -count=1 -timeout=90s

go test ./internal/server -run '^(TestAdminCanClearAllAPIKeyLimits|TestAdminCanClearAllAPIKeyLimitsAfterApproval|TestPriceUsageAllowsConfiguredFreeCacheWrites)$' -count=1 -timeout=90s
```

两组均通过，分别报告 `0.625s`、`0.637s` 的测试执行时间，不含编译/调用开销。合计 8 项已有测试，覆盖：

- OpenAI 上游转 Anthropic 流式时缓存用量回传及内部持久化断言，以及原生 Anthropic 流式用量。
- 用户跨 Key token 预留与重复结算。
- 现有按请求开始时间选择输入/输出时段价格。
- API Key 全零限额直接更新及审批后更新。
- 明确配置为免费的缓存写入。

不构成以下验证：免费缓存读取已经修好；新的星期及缓存分项时段覆盖；多实例金额预留；#304/#221 当前浏览器复现；供应商真实账单一致；完整后端/前端测试及生产部署。本次仅按研究范围进行聚焦回归，不执行修改任务所需的全部门禁。

## 4. 建议交付分组

1. **计量与价格合同**：#264 主线，#208/#138 回归约束，#221 配置链路验收。覆盖同源 usage、全部分项分时价格、明确免费及完整保存回读。
2. **预算、尝试与对账**：#259 的金额扩展、#246 中计费正确性子集、#76/#77 接续。覆盖多主体预留、幂等、故障恢复和差额调整。
3. **归属与可解释展示**：#304 复现与修复，联动费用明细、名称快照与权限。修复完成的判断来自复现场景与验收证据，不来自把它列入方案。

这三组属于同一设计范围，可分阶段实施和验证。#177/#178、#246 的完整性能改造，以及模型库存删除管理独立跟踪。本次没有实施修复，也没有变更 GitHub issue 状态。
