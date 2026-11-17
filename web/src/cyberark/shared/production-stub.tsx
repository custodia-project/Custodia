/* Build-time replacement for the legacy CyberArk/Netskope/Personas lazy
   modules in the production profile. It keeps the import graph small without
   changing the demo build. */
type AnyProps = Record<string, unknown>;
export function SiaHubWorkspace(_: AnyProps) { return null; }
export function IdentityWorkspace(_: AnyProps) { return null; }
export function PersonChainsWorkspace(_: AnyProps) { return null; }
export function ConnectionInventoryWorkspace(_: AnyProps) { return null; }
export function PostureWorkspace(_: AnyProps) { return null; }
