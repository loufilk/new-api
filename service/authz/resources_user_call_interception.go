package authz

const ResourceUserCallInterception = "user_call_interception"

var (
	UserCallInterceptionRead   = Permission{Resource: ResourceUserCallInterception, Action: ActionRead}
	UserCallInterceptionUpdate = Permission{Resource: ResourceUserCallInterception, Action: "update"}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceUserCallInterception,
		LabelKey: "User-level call interception and custom replies",
		Actions: []ActionDefinition{
			{Action: ActionRead, LabelKey: "View call interception configuration and logs", DescriptionKey: "View interception configuration, hits, and configuration audits for manageable users.", DefaultRoles: []string{BuiltInRoleAdmin}},
			{Action: "update", LabelKey: "Update call interception configuration", DescriptionKey: "Enable, edit, or disable custom replies for manageable users.", DefaultRoles: []string{BuiltInRoleAdmin}},
		},
	})
}
