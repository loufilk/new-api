package authz

var (
	CommerceInvoiceRead    = Permission{Resource: "commerce_invoices", Action: ActionRead}
	CommerceInvoiceReview  = Permission{Resource: "commerce_invoices", Action: "review"}
	CommercePartnerRead    = Permission{Resource: "commerce_partners", Action: ActionRead}
	CommercePartnerReview  = Permission{Resource: "commerce_partners", Action: "review"}
	CommerceSettingsRead   = Permission{Resource: "commerce_settings", Action: ActionRead}
	CommerceSettingsUpdate = Permission{Resource: "commerce_settings", Action: "update"}
)

func init() {
	RegisterResource(ResourceDefinition{Resource: "commerce_invoices", LabelKey: "Invoice applications", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View invoice applications", DescriptionKey: "View submitted invoice details and order snapshots.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "review", LabelKey: "Review invoice applications", DescriptionKey: "Review invoices, record fee receipts and deliver issued invoices.", DefaultRoles: []string{BuiltInRoleAdmin}},
	}})
	RegisterResource(ResourceDefinition{Resource: "commerce_partners", LabelKey: "Partner applications", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View partner applications", DescriptionKey: "View partner contact details and submitted quotes.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "review", LabelKey: "Review partner applications", DescriptionKey: "Review partner applications and publish review notes.", DefaultRoles: []string{BuiltInRoleAdmin}},
	}})
	RegisterResource(ResourceDefinition{Resource: "commerce_settings", LabelKey: "Commerce settings", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View commerce settings", DescriptionKey: "View invoice and partnership application rules.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "update", LabelKey: "Update commerce settings", DescriptionKey: "Configure invoice eligibility, service fees and partner applications.", DefaultRoles: []string{BuiltInRoleAdmin}},
	}})
}
