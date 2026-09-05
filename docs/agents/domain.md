# Domain Docs

## Layout

采用 single-context 布局：

- CONTEXT.md：仓库级领域术语表。
- docs/adr/：仓库级架构决策记录。

## Reading rules

探索代码前，先阅读根目录 CONTEXT.md，
再阅读 docs/adr/ 中与当前工作相关的决策。

若以后存在 CONTEXT-MAP.md，先读取映射，
再阅读相关上下文的 CONTEXT.md 及上下文级 ADR。

若文档不存在，静默继续，不因缺失而阻断工作或提前建议创建。
领域术语或决策真正明确后，由 domain-modeling 按需补充。

## Vocabulary

Issue、方案、假设和测试中涉及领域概念时，
使用 CONTEXT.md 中定义的术语，避免其中明确排除的同义表达。
测试名称仍须遵循 AGENTS.md 的英文要求。

若所需概念尚未定义，先判断是否引入了不必要的新术语；
确有缺口时，记录为 domain-modeling 的待处理项。

## ADR conflicts

若建议与现有 ADR 冲突，明确指出 ADR 编号、
冲突内容和重新讨论的理由，不静默覆盖已有决策。
