package metrics

import "custodia/server/internal/connectors"

// Units are stable codes; the interface maps them to labels.
const (
	UnitSafes     = "safes"
	UnitAccounts  = "accounts"
	UnitPlatforms = "platforms"
	UnitAgents    = "agents"
	UnitGroups    = "groups"
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
		ID: "wazuh.agents.total.v1", Connector: connectors.Wazuh,
		Name:        "Agentes totales",
		Description: "Agentes registrados en el manager en la última publicación de la colección agents.",
		Collection:  "agents", Kind: KindCount, Unit: UnitAgents,
	},
	{
		ID: "wazuh.agents.active.v1", Connector: connectors.Wazuh,
		Name:        "Agentes activos",
		Description: "Agentes con status active. Un agente sin ese dato no se cuenta.",
		Collection:  "agents", Kind: KindCountWhere, Unit: UnitAgents,
		Where: &Condition{Field: "status", Values: []string{"active"}},
	},
	{
		ID: "wazuh.agents.pending.v1", Connector: connectors.Wazuh,
		Name:        "Agentes pendientes",
		Description: "Agentes con status pending.",
		Collection:  "agents", Kind: KindCountWhere, Unit: UnitAgents,
		Where: &Condition{Field: "status", Values: []string{"pending"}},
	},
	{
		ID: "wazuh.agents.disconnected.v1", Connector: connectors.Wazuh,
		Name:        "Agentes desconectados",
		Description: "Agentes con status disconnected. No mide la causa ni si el equipo sigue existiendo.",
		Collection:  "agents", Kind: KindCountWhere, Unit: UnitAgents,
		Where: &Condition{Field: "status", Values: []string{"disconnected"}},
	},
	{
		ID: "wazuh.agents.never_connected.v1", Connector: connectors.Wazuh,
		Name:        "Agentes nunca conectados",
		Description: "Agentes con status never_connected.",
		Collection:  "agents", Kind: KindCountWhere, Unit: UnitAgents,
		Where: &Condition{Field: "status", Values: []string{"never_connected"}},
	},
	{
		ID: "wazuh.agents.stale_7d.v1", Connector: connectors.Wazuh,
		Name:        "Agentes sin contacto hace más de 7 días",
		Description: "Agentes cuyo last_keep_alive es anterior a 7 días respecto de la publicación. Un agente sin ese dato no se cuenta.",
		Collection:  "agents", Kind: KindAgeOver, Unit: UnitAgents,
		AgeField: "last_keep_alive", ThresholdDays: 7,
	},
	{
		ID: "wazuh.groups.total.v1", Connector: connectors.Wazuh,
		Name:        "Grupos totales",
		Description: "Grupos declarados en el manager en la última publicación de la colección groups.",
		Collection:  "groups", Kind: KindCount, Unit: UnitGroups,
	},
	{
		ID: "wazuh.groups.empty.v1", Connector: connectors.Wazuh,
		Name:        "Grupos sin agentes",
		Description: "Grupos con agent_count = 0. Un grupo sin ese dato no se cuenta.",
		Collection:  "groups", Kind: KindCountWhere, Unit: UnitGroups,
		Where: &Condition{Field: "agent_count", Values: []string{"0"}},
	},
	{
		ID: "cyberark-pam.platforms.psm_unresolved.v1", Connector: connectors.CyberArkPAM,
		Name:        "Plataformas con PSM sin resolver",
		Description: "Vínculos plataforma→PSM de platform_psm_links con resolución missing (el PSM referido no está en el catálogo) o ambiguous (varios candidatos que sólo difieren en mayúsculas).",
		Collection:  "platform_psm_links", Kind: KindCountWhere, Unit: UnitPlatforms,
		Where: &Condition{Field: "resolution", Values: []string{"missing", "ambiguous"}},
	},
}
