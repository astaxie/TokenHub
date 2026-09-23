import { type SchemaEvolutionStatus } from "../core/types";
import { formatLocaleNumber, formatTranslationTemplate, tx } from "./runtime";

export type RollbackCompatibilityReason = {
  compatibility_reason_code?: string;
  compatibility_reason_params?: {
    version?: string;
    state?: number;
    release?: string;
    min?: number;
    max?: number;
  };
};

export function evolutionReasonText(schema: SchemaEvolutionStatus): string {
  switch (schema.reason_code) {
    case "handle_unavailable":
      return tx("无法访问数据库连接；请查看服务器日志");
    case "runner_error":
      return tx("无法初始化迁移运行器；请查看服务器日志");
    case "ledger_unreadable":
      return tx("无法读取迁移账本；请查看服务器日志");
    case "baseline_missing":
      return tx("数据库尚未记录采纳基线；请先启动一次服务器完成采纳");
    case "heartbeat_failing":
      return tx("实例心跳未发布；contract 维护无法发现该实例");
    case "dirty_migration":
      return formatTranslationTemplate(tx("版本 {version} 的迁移处于脏状态，需要修复"), {
        version: formatLocaleNumber(schema.dirty_version ?? schema.schema_version),
      });
    case "ledger_verification_failed":
      return tx("迁移账本校验失败");
    case "expand_pending":
      return tx("存在待执行的 expand 迁移；请运行 tokenhub db migrate 或重启服务器");
    case "backfill_ledger_unreadable":
      return tx("无法读取数据回填账本；请查看服务器日志");
    case "blocking_backfills_pending":
      return formatTranslationTemplate(tx("阻塞型数据回填未完成：{ids}"), {
        ids: (schema.blocking_backfills_pending ?? []).join(", "),
      });
    default:
      return tx("数据库演进状态不可用；请查看服务器日志");
  }
}

export function rollbackCompatibilityReasonText(reason: RollbackCompatibilityReason): string {
  const params = reason.compatibility_reason_params ?? {};
  switch (reason.compatibility_reason_code) {
    case "requested_version_invalid":
      return formatTranslationTemplate(tx("无法识别回退版本 {version}"), {
        version: String(params.version ?? ""),
      });
    case "compatibility_record_missing":
      return formatTranslationTemplate(tx("版本 {version} 没有经过验证的数据库兼容性记录"), {
        version: String(params.version ?? ""),
      });
    case "database_evolution_not_clean":
      return tx("数据库演进状态未就绪，暂时无法回退");
    case "database_version_outside_range":
      return formatTranslationTemplate(tx("数据库状态版本 {state} 超出版本 {release} 的兼容范围 {min} – {max}"), {
        state: params.state === undefined ? "" : formatLocaleNumber(params.state),
        release: String(params.release ?? ""),
        min: params.min === undefined ? "" : formatLocaleNumber(params.min),
        max: params.max === undefined ? "" : formatLocaleNumber(params.max),
      });
    default:
      return tx("数据库兼容性检查未通过");
  }
}
