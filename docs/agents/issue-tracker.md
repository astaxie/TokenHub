# Issue tracker: GitHub

本仓库的需求、问题及规格记录在 astaxie/TokenHub 的 GitHub Issues。
使用 gh CLI；在命令中指定 --repo astaxie/TokenHub。

## Conventions

- 创建前搜索 open 和 closed issues，避免重复。
- 遵循最匹配的 .github/ISSUE_TEMPLATE/ 表单，保留必填章节。
- Issue 和 PR 的标题、正文使用英文。
- 多行正文写入临时文件，通过 --body-file 提交。
- 不包含凭据、令牌、Cookie、未脱敏环境文件或其他秘密。
- 发布、评论及其他外部写入必须在用户授权范围内执行。

常用命令：

- 查看：gh issue view <number> --repo astaxie/TokenHub --comments
- 搜索：gh issue list --repo astaxie/TokenHub --state all --search "<query>"
- 列出：gh issue list --repo astaxie/TokenHub --state open --json number,title,body,labels,comments
- 创建：gh issue create --repo astaxie/TokenHub --title "<title>" --body-file <file>
- 评论：gh issue comment <number> --repo astaxie/TokenHub --body-file <file>
- 添加标签：gh issue edit <number> --repo astaxie/TokenHub --add-label "<label>"
- 移除标签：gh issue edit <number> --repo astaxie/TokenHub --remove-label "<label>"
- 关闭：gh issue close <number> --repo astaxie/TokenHub

## Pull requests as a triage surface

**PRs as a request surface: no.**

GitHub Issues 与 PR 共用编号空间。遇到类型不明确的编号，
先通过 gh pr view 判断；未找到时再用 gh issue view，
并区分对象不存在与认证或网络故障。

## Skill operations

技能要求“publish to the issue tracker”时，创建 GitHub Issue。
要求“fetch the relevant ticket”时，读取 Issue 正文、标签及评论。

## Wayfinding operations

- Map：使用带 wayfinder:map 标签的 Issue，记录 Notes、
  Decisions-so-far 和 Fog。
- 子任务：优先使用 GitHub sub-issues；不可用时，在 Map 正文维护
  任务列表，并在子任务正文注明 Part of #<map>。
- 类型标签：wayfinder:research、wayfinder:prototype、
  wayfinder:grilling、wayfinder:task。
- 阻塞关系：优先使用 GitHub 原生 issue dependencies；
  不可用时在子任务正文维护 Blocked by: #<number>。
- 可执行任务：按 Map 顺序选择未关闭、无未关闭阻塞项且无人认领的子任务。
- 认领：将任务分配给实际负责的开发者。
- 完成：记录结果、关闭任务，并在 Map 的 Decisions-so-far 中添加摘要和链接。
