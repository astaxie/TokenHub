# 将一次性数据回填与持续一致性修复分离

TokenHub 只把具有永久完成条件的数据转换放入 data backfill ledger，并按运行时不变量区分阻塞式与在线执行。结构、request-log trigger/index、routing binding 唯一性和 Provider adapter topology 阻塞就绪；request-log 历史 sequence、request attribution、synthetic-DNS defaults、Codex image route 以及补齐兼容读写后的 team relationship 可以在线回填。

Unfinished image job recovery、默认与 catalog 同步、route 到 provider inventory、external model role 等操作会因进程中断或后续写入再次出现偏差，因此保留为持续一致性修复，不获得一次性 migration version。这样可以避免 ledger 显示“已完成”后，系统却失去继续维护不变量的路径。
