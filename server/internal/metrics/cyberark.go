package metrics

import "custodia/server/internal/connectors"

// Units are stable codes; the interface maps them to labels.
const (
	UnitSafes     = "safes"
	UnitAccounts  = "accounts"
	UnitPlatforms = "platforms"
	UnitPercent   = "percent"
)

// declared is the static catalog. Field names are the normalized attributes
// of the CyberArk PAM adapter allowlist (internal/inventory), not provider
// JSON names; Validate enforces it.
var declared = []Definition{
	{
		ID: "cyberark-pam.safes.total.v1", Connector: connectors.CyberArkPAM,
		Name:        "Safes totales",
		Description: "Safes activos en la última publicación de la colección safes de esta conexión.",
		Collection:  "safes", Kind: KindCount, Unit: UnitSafes,
	},
	{
		ID: "cyberark-pam.safes.without_members.v1", Connector: connectors.CyberArkPAM,
		Name:        "Safes sin miembros",
		Description: "Safes de la colección safes cuyo número no aparece en ningún miembro (safe_number) de la colección safe_members de la misma conexión. Requiere ambas colecciones; si safe_members es parcial el valor no se calcula, porque faltarían miembros.",
		Collection:  "safes", Kind: KindCountWithout, Unit: UnitSafes,
		Without: &Reference{Collection: "safe_members", Field: "safe_number"},
	},
	{
		ID: "cyberark-pam.accounts.total.v1", Connector: connectors.CyberArkPAM,
		Name:        "Cuentas totales",
		Description: "Cuentas activas (metadatos sin secretos) en la última publicación de la colección accounts.",
		Collection:  "accounts", Kind: KindCount, Unit: UnitAccounts,
	},
	{
		ID: "cyberark-pam.accounts.auto_management_disabled.v1", Connector: connectors.CyberArkPAM,
		Name:        "Cuentas con gestión automática deshabilitada",
		Description: "Cuentas con automatic_management_enabled = false. Una cuenta sin ese dato no se cuenta.",
		Collection:  "accounts", Kind: KindCountWhere, Unit: UnitAccounts,
		Where: &Condition{Field: "automatic_management_enabled", Values: []string{"false"}},
	},
	{
		ID: "cyberark-pam.accounts.auto_management_disabled_ratio.v1", Connector: connectors.CyberArkPAM,
		Name:        "Proporción de cuentas sin gestión automática",
		Description: "Cuentas con gestión automática deshabilitada sobre las cuentas que informan ese dato (true o false). No se calcula sobre una publicación parcial.",
		Collection:  "accounts", Kind: KindRatio, Unit: UnitPercent,
		Where:       &Condition{Field: "automatic_management_enabled", Values: []string{"false"}},
		Denominator: &Condition{Field: "automatic_management_enabled", Values: []string{"true", "false"}},
	},
	{
		ID: "cyberark-pam.accounts.last_change_failed.v1", Connector: connectors.CyberArkPAM,
		Name:        "Cuentas con último cambio fallido",
		Description: "Cuentas cuyo management_status normalizado es failure. No mide la causa ni si el secreto sigue siendo válido.",
		Collection:  "accounts", Kind: KindCountWhere, Unit: UnitAccounts,
		Where: &Condition{Field: "management_status", Values: []string{"failure"}},
	},
	{
		ID: "cyberark-pam.platforms.inactive.v1", Connector: connectors.CyberArkPAM,
		Name:        "Plataformas inactivas",
		Description: "Plataformas con active = false. Una plataforma sin ese dato no se cuenta.",
		Collection:  "platforms", Kind: KindCountWhere, Unit: UnitPlatforms,
		Where: &Condition{Field: "active", Values: []string{"false"}},
	},
	{
		ID: "cyberark-pam.platforms.psm_unresolved.v1", Connector: connectors.CyberArkPAM,
		Name:        "Plataformas con PSM sin resolver",
		Description: "Vínculos plataforma→PSM de platform_psm_links con resolución missing (el PSM referido no está en el catálogo) o ambiguous (varios candidatos que sólo difieren en mayúsculas).",
		Collection:  "platform_psm_links", Kind: KindCountWhere, Unit: UnitPlatforms,
		Where: &Condition{Field: "resolution", Values: []string{"missing", "ambiguous"}},
	},
}
