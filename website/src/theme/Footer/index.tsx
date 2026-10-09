import React from 'react';
import { translate } from '@docusaurus/Translate';
import { useThemeConfig } from '@docusaurus/theme-common';
import FooterLinks from '@theme/Footer/Links';
import FooterLogo from '@theme/Footer/Logo';
import FooterCopyright from '@theme/Footer/Copyright';
import FooterLayout from '@theme/Footer/Layout';

// Swizzle of the classic theme footer: identical rendering, but the link
// column titles, link labels, and copyright are passed through translate()
// so each locale build picks up its value from i18n/<locale>/code.json
// (written by scripts/sync-docs.mjs from its FOOTER_TRANSLATIONS table).
// The keys are the English strings configured in docusaurus.config.ts;
// unmapped strings render as-is.
const LINK_COLUMN_IDS: Record<string, string> = {
  Documentation: 'footer.linkColumns.Documentation',
  Platform: 'footer.linkColumns.Platform',
  Community: 'footer.linkColumns.Community',
};

const LINK_LABEL_IDS: Record<string, string> = {
  Architecture: 'footer.links.Architecture',
  'User Guide': 'footer.links.UserGuide',
  'Administrator Guide': 'footer.links.AdministratorGuide',
  Deployment: 'footer.links.Deployment',
  'Billing and Pricing': 'footer.links.BillingAndPricing',
  'Agent Token Cost API': 'footer.links.AgentTokenCostAPI',
  'Plugin Development': 'footer.links.PluginDevelopment',
  Migration: 'footer.links.Migration',
  GitHub: 'footer.links.GitHub',
  Issues: 'footer.links.Issues',
  'Apache-2.0 License': 'footer.links.ApacheLicense',
};

function localizeColumnTitle(title: string): string {
  const id = LINK_COLUMN_IDS[title];
  return id ? translate({ id, message: title }) : title;
}

function localizeLinkLabel(label: string): string {
  const id = LINK_LABEL_IDS[label];
  return id ? translate({ id, message: label }) : label;
}

function Footer() {
  const { footer } = useThemeConfig();
  if (!footer) {
    return null;
  }
  const { copyright, links, logo, style } = footer;
  return (
    <FooterLayout
      style={style}
      links={
        links &&
        links.length > 0 && (
          <FooterLinks
            links={links.map((column) => ({
              ...column,
              title: localizeColumnTitle(column.title),
              items: column.items.map((item) =>
                item.label
                  ? { ...item, label: localizeLinkLabel(item.label) }
                  : item,
              ),
            }))}
          />
        )
      }
      logo={logo && <FooterLogo logo={logo} />}
      copyright={
        copyright && (
          <FooterCopyright
            copyright={translate(
              { id: 'footer.copyright', message: copyright },
              { year: new Date().getFullYear().toString() },
            )}
          />
        )
      }
    />
  );
}

export default React.memo(Footer);
