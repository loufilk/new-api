package authz

var (
	PartnerApplicationRead   = Permission{Resource: "partner_applications", Action: ActionRead}
	PartnerApplicationReview = Permission{Resource: "partner_applications", Action: "review"}
	PartnerSettingsRead      = Permission{Resource: "partner_settings", Action: ActionRead}
	PartnerSettingsUpdate    = Permission{Resource: "partner_settings", Action: "update"}
)

func init() {
	RegisterResource(ResourceDefinition{Resource: "partner_applications", LabelKey: "Partner applications", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View partner applications", DescriptionKey: "View partner contact details and submitted quotes.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "review", LabelKey: "Review partner applications", DescriptionKey: "Review partner applications and publish review notes.", DefaultRoles: []string{BuiltInRoleAdmin}},
	}})
	RegisterResource(ResourceDefinition{Resource: "partner_settings", LabelKey: "Partner settings", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View partner settings", DescriptionKey: "View partner application rules.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "update", LabelKey: "Update partner settings", DescriptionKey: "Configure partner application intake and instructions.", DefaultRoles: []string{BuiltInRoleAdmin}},
	}})
}
