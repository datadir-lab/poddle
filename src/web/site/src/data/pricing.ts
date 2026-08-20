// Pricing data loader.
//
// Canonical pricing lives in the private poddle-cloud repo (pricing/pricing.json)
// and is injected at deploy time as `pricing.generated.json` (gitignored) by
// scripts/sync-pricing.mjs. When that file is absent - local dev, the open-source
// build, a fork - we fall back to the committed placeholder `pricing.json`, whose
// paid amounts are redacted ($XX) on purpose. This keeps commercial pricing out of
// the AGPL repo while the marketing page still renders.

export interface PricingCta {
  label: string;
  href: string;
  variant: 'primary' | 'ghost';
}

export interface PricingTier {
  name: string;
  tag: string;
  amount: string;
  period: string;
  note: string;
  featured: boolean;
  cta: PricingCta;
  feats: string[];
}

export interface PodRate {
  size: string;
  specs: string;
  price: string;
  unit: string;
}

export interface ManagedPods {
  comingSoon?: boolean;
  heading: string;
  sub: string;
  note: string;
  cta?: PricingCta;
  rates?: PodRate[];
}

export interface ByoCompute {
  heading: string;
  sub: string;
  feats: string[];
}

export interface Faq {
  q: string;
  a: string;
}

export interface Pricing {
  _comment?: string;
  currency: string;
  tiers: PricingTier[];
  podCompute: { managed: ManagedPods; byo: ByoCompute };
  included: string[];
  faqs: Faq[];
}

// Prefer the deploy-injected generated file; fall back to the committed
// placeholder. import.meta.glob only matches files that exist on disk, so the
// generated key is simply absent on a standalone build.
const files = import.meta.glob<{ default: Pricing }>('./pricing*.json', { eager: true });
const generated = files['./pricing.generated.json'];
const placeholder = files['./pricing.json'];
const chosen = generated ?? placeholder;

if (!chosen?.default?.tiers?.length) {
  throw new Error('pricing: no valid data found (expected src/data/pricing.json)');
}

export const pricing: Pricing = chosen.default;
export const pricingSource: 'generated' | 'placeholder' = generated ? 'generated' : 'placeholder';
