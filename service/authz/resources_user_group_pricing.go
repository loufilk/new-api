package authz

const ResourceUserGroupPricing = "user_group_pricing"

var (
	UserGroupPricingRead   = Permission{Resource: ResourceUserGroupPricing, Action: ActionRead}
	UserGroupPricingUpdate = Permission{Resource: ResourceUserGroupPricing, Action: "update"}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceUserGroupPricing,
		LabelKey: "User group pricing",
		Actions: []ActionDefinition{
			{Action: ActionRead, LabelKey: "View user group pricing", DescriptionKey: "View independent group rates for manageable users.", DefaultRoles: []string{BuiltInRoleAdmin}},
			{Action: "update", LabelKey: "Update user group pricing", DescriptionKey: "Set or clear independent group rates for manageable users.", DefaultRoles: []string{BuiltInRoleAdmin}},
		},
	})
}
