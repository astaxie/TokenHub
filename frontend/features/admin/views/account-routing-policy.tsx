import type { Model } from "../core/types";
import type { AccountRoutingStrategy } from "../core/account-allowance-types";
import { tx } from "../i18n/runtime";

const strategies: AccountRoutingStrategy[] = ["default", "quota_smart", "expiry_first", "least_used", "drain"];

export function readAccountStrategy(model: Model): AccountRoutingStrategy {
  const value = model.metadata?.tokenhub_account_strategy as AccountRoutingStrategy | undefined;
  return value && strategies.includes(value) ? value : "default";
}

export function AccountRoutingFields({ value, onChange, disabled }: {
  value: AccountRoutingStrategy;
  onChange: (strategy: AccountRoutingStrategy) => void;
  disabled: boolean;
}) {
  const options = [
    { value: "default", label: tx("沿用原有账号顺序"), help: tx("保持现有账号权重、优先级和会话绑定行为。") },
    { value: "quota_smart", label: tx("额度智能"), help: tx("优先使用已确认可用且较早重置的账号，先比较较长的额度周期。") },
    { value: "expiry_first", label: tx("到期优先"), help: tx("优先使用额度或套餐较早到期的可用账号。") },
    { value: "least_used", label: tx("最少使用"), help: tx("在已确认可用的账号中，优先使用最近一小时实际调用 Token 较少的账号。") },
    { value: "drain", label: tx("用完再换"), help: tx("保持当前账号，确认额度耗尽后再换号；临时故障不会永久改变顺序。") },
  ];
  return <div className="account-routing-policy">
    <label>
      <span>{tx("账号调度")}</span>
      <select disabled={disabled} value={value} onChange={event => onChange(event.target.value as AccountRoutingStrategy)}>
        {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </label>
    <p>{options.find(option => option.value === value)?.help}</p>
    <small>{tx("仅调整同一 Provider 线路、同一账号优先级内的已授权账号；额度仅读取后台缓存。")}</small>
  </div>;
}
