import { enTranslations } from "./en";
import { jaTranslations } from "./ja";
import { modelGovernanceTranslations } from "./model-governance";
import { playgroundTranslations } from "./playground";
import { providerConnectionTranslations } from "./provider-connection";
import { routingTranslations } from "./routing";
import { scopedRoutingPolicyTranslations } from "./scoped-routing-policy";
import { securityTranslations } from "./security";
import { usageTranslations } from "./usage";
import { agentTranslations } from "./agents";

export const translations: Record<"en" | "ja", Record<string, string>> = {
	en: { ...enTranslations, ...routingTranslations.en, ...scopedRoutingPolicyTranslations.en, ...modelGovernanceTranslations.en, ...providerConnectionTranslations.en, ...usageTranslations.en, ...playgroundTranslations.en, ...securityTranslations.en, ...agentTranslations.en },
	ja: { ...jaTranslations, ...routingTranslations.ja, ...scopedRoutingPolicyTranslations.ja, ...modelGovernanceTranslations.ja, ...providerConnectionTranslations.ja, ...usageTranslations.ja, ...playgroundTranslations.ja, ...securityTranslations.ja, ...agentTranslations.ja },
};
