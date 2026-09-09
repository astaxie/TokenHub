import { enTranslations } from "./en";
import { jaTranslations } from "./ja";
import { ruTranslations } from "./ru";
import { adminUICopyTranslations } from "./admin-ui-copy";
import { billingStatementTranslations } from "./billing-statements";
import { billingPricingTranslations } from "./billing-pricing";
import { adminWorkflowTranslations } from "./admin-workflows";
import { apiKeyUsageTranslations } from "./api-key-usage";
import { auditFilterTranslations } from "./audit-filters";
import { dbEvolutionTranslations } from "./db-evolution";
import { loginHomeTranslations } from "./login-home";
import { modelGovernanceTranslations } from "./model-governance";
import { gatewayDocsTranslations } from "./gateway-docs";
import { playgroundTranslations } from "./playground";
import { pluginTranslations } from "./plugins";
import { providerConnectionTranslations } from "./provider-connection";
import { providerMonitoringTranslations } from "./provider-monitoring";
import { routingTranslations } from "./routing";
import { codexImageTranslations } from "./codex-image";
import { scopedRoutingPolicyTranslations } from "./scoped-routing-policy";
import { securityTranslations } from "./security";
import { usageTranslations } from "./usage";
import { notificationTranslations } from "./notifications";
import syntheticDNSTranslations from "./synthetic-dns";

export const translations: Record<"en" | "ja" | "ru", Record<string, string>> = {
  en: { ...adminUICopyTranslations.en, ...billingStatementTranslations.en, ...billingPricingTranslations.en, ...enTranslations, ...adminWorkflowTranslations.en, ...apiKeyUsageTranslations.en, ...auditFilterTranslations.en, ...dbEvolutionTranslations.en, ...routingTranslations.en, ...codexImageTranslations.en, ...scopedRoutingPolicyTranslations.en, ...modelGovernanceTranslations.en, ...gatewayDocsTranslations.en, ...loginHomeTranslations.en, ...providerConnectionTranslations.en, ...providerMonitoringTranslations.en, ...usageTranslations.en, ...playgroundTranslations.en, ...pluginTranslations.en, ...securityTranslations.en, ...notificationTranslations.en, ...syntheticDNSTranslations.en },
  ja: { ...adminUICopyTranslations.ja, ...billingStatementTranslations.ja, ...billingPricingTranslations.ja, ...jaTranslations, ...adminWorkflowTranslations.ja, ...apiKeyUsageTranslations.ja, ...auditFilterTranslations.ja, ...dbEvolutionTranslations.ja, ...routingTranslations.ja, ...codexImageTranslations.ja, ...scopedRoutingPolicyTranslations.ja, ...modelGovernanceTranslations.ja, ...gatewayDocsTranslations.ja, ...loginHomeTranslations.ja, ...providerConnectionTranslations.ja, ...providerMonitoringTranslations.ja, ...usageTranslations.ja, ...playgroundTranslations.ja, ...pluginTranslations.ja, ...securityTranslations.ja, ...notificationTranslations.ja, ...syntheticDNSTranslations.ja },
  ru: { ...adminUICopyTranslations.ru, ...billingStatementTranslations.ru, ...billingPricingTranslations.ru, ...ruTranslations, ...adminWorkflowTranslations.ru, ...apiKeyUsageTranslations.ru, ...auditFilterTranslations.ru, ...dbEvolutionTranslations.ru, ...routingTranslations.ru, ...codexImageTranslations.ru, ...scopedRoutingPolicyTranslations.ru, ...modelGovernanceTranslations.ru, ...gatewayDocsTranslations.ru, ...loginHomeTranslations.ru, ...providerConnectionTranslations.ru, ...providerMonitoringTranslations.ru, ...usageTranslations.ru, ...playgroundTranslations.ru, ...pluginTranslations.ru, ...securityTranslations.ru, ...notificationTranslations.ru, ...syntheticDNSTranslations.ru },
};
