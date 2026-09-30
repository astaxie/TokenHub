import { retrievalGuidanceTranslations } from "./retrieval-guidance";
import { rerankTranslations } from "./rerank";
import { embeddingTranslations } from "./embeddings";
import { semanticRoutingTranslations } from "./semantic-routing";
import { apiKeyAccessTranslations } from "./api-key-access";
import { adminUICopyTranslations } from "./admin-ui-copy";
import { billingStatementTranslations } from "./billing-statements";
import { billingPricingTranslations } from "./billing-pricing";
import { enTranslations } from "./en";
import { jaTranslations } from "./ja";
import { ruTranslations } from "./ru";
import { adminResourcesRuTranslations } from "./admin-resources-ru";
import { adminDomainRuTranslations } from "./admin-domain-ru";
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
  en: { ...retrievalGuidanceTranslations.en, ...rerankTranslations.en, ...embeddingTranslations.en, ...semanticRoutingTranslations.en, ...apiKeyAccessTranslations.en, ...adminUICopyTranslations.en, ...billingStatementTranslations.en, ...billingPricingTranslations.en, ...enTranslations, ...adminWorkflowTranslations.en, ...apiKeyUsageTranslations.en, ...auditFilterTranslations.en, ...dbEvolutionTranslations.en, ...routingTranslations.en, ...codexImageTranslations.en, ...scopedRoutingPolicyTranslations.en, ...modelGovernanceTranslations.en, ...gatewayDocsTranslations.en, ...loginHomeTranslations.en, ...providerConnectionTranslations.en, ...providerMonitoringTranslations.en, ...usageTranslations.en, ...playgroundTranslations.en, ...pluginTranslations.en, ...securityTranslations.en, ...notificationTranslations.en, ...syntheticDNSTranslations.en },
  ja: { ...retrievalGuidanceTranslations.ja, ...rerankTranslations.ja, ...embeddingTranslations.ja, ...semanticRoutingTranslations.ja, ...apiKeyAccessTranslations.ja, ...adminUICopyTranslations.ja, ...billingStatementTranslations.ja, ...billingPricingTranslations.ja, ...jaTranslations, ...adminWorkflowTranslations.ja, ...apiKeyUsageTranslations.ja, ...auditFilterTranslations.ja, ...dbEvolutionTranslations.ja, ...routingTranslations.ja, ...codexImageTranslations.ja, ...scopedRoutingPolicyTranslations.ja, ...modelGovernanceTranslations.ja, ...gatewayDocsTranslations.ja, ...loginHomeTranslations.ja, ...providerConnectionTranslations.ja, ...providerMonitoringTranslations.ja, ...usageTranslations.ja, ...playgroundTranslations.ja, ...pluginTranslations.ja, ...securityTranslations.ja, ...notificationTranslations.ja, ...syntheticDNSTranslations.ja },
  ru: { ...retrievalGuidanceTranslations.ru, ...rerankTranslations.ru, ...embeddingTranslations.ru, ...semanticRoutingTranslations.ru, ...apiKeyAccessTranslations.ru, ...adminUICopyTranslations.ru, ...ruTranslations, ...adminResourcesRuTranslations, ...adminDomainRuTranslations, ...modelGovernanceTranslations.ru },
};
