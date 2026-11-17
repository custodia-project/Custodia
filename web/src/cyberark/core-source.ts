import type { FrontendRegistration, RuntimeRegistration } from '../source-contract.ts';
import { t } from '../cross/i18n/index.ts';

const runtime: RuntimeRegistration = {
  id: 'cyberark', refreshDomain: 'pam',
  load: () => import('./shared/router.tsx').then((module) => ({ default: module.CyberarkRouter })),
  gateWorkspace: false, suspense: true,
};

/** Production CyberArk domain: PAM only. SIA, Risk, Identity and Personas are
 *  removed by the build-time source/import profile. */
export const registration = {
  id: 'cyberark/core',
  workspaces: [],
  domains: [{ id: 'pam', order: 1, label: t('CyberArk'), blurb: t('PAM'), status: 'active', access: 'domain', sectionNav: true, product: { id: 'cyberark', label: t('CyberArk'), description: t('PAM') } }],
  categories: [],
  runtime,
} as const satisfies FrontendRegistration;
