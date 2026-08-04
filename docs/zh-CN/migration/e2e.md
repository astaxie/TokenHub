# E2E 测试

迁移框架提供基于 Docker Compose 的端到端测试。测试会启动真实 LiteLLM 和 TokenHub 服务，并通过 TokenHub Admin API 验证完整的 extract → plan → apply → verify → re-apply → rollback 流程。上游 OpenAI 兼容接口由 Nginx 模拟，用户导入所需的 SMTP 服务由 Mailpit 提供。

## 运行方式

```bash
# 启动服务
docker compose -f deploy/docker-compose.migration-e2e.yml up -d --wait

# 执行测试
cd sdk/migration-e2e && npm ci && npm run test:litellm

# 清理
docker compose -f deploy/docker-compose.migration-e2e.yml down -v
```

## CI 触发

E2E 测试在 CI 中通过添加 `migration:e2e` 标签触发。

测试会确认重复 apply 的创建数和更新数均为 0、checkpoint 与一次性 API key 文件已保存，并确认 rollback 会删除新建用户。详细说明请参阅 `docs/migration/e2e.md`。
