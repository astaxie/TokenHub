import type { StatementSide } from "../../../features/admin/domain/billing-statements";
import type { StatementState } from "../fixtures/billing";

export const statements = [
  { id: "customer-complete", title: "客户对账单与分项依据", side: "tenant", state: "complete" },
  { id: "provider-pending", title: "上游成本：已知、免费与待核实", side: "provider", state: "pending" },
  { id: "margin-complete", title: "预计毛利汇总", side: "margin", state: "complete" },
  { id: "statement-empty", title: "对账单空状态", side: "tenant", state: "empty" },
] satisfies { id: string; title: string; side: StatementSide; state: StatementState }[];
