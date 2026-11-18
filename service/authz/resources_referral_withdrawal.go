package authz

var (
	ReferralWithdrawalRead           = Permission{Resource: "referral_withdrawals", Action: ActionRead}
	ReferralWithdrawalReview         = Permission{Resource: "referral_withdrawals", Action: "review"}
	ReferralWithdrawalPay            = Permission{Resource: "referral_withdrawals", Action: "pay"}
	ReferralWithdrawalSettingsRead   = Permission{Resource: "referral_withdrawal_settings", Action: ActionRead}
	ReferralWithdrawalSettingsUpdate = Permission{Resource: "referral_withdrawal_settings", Action: "update"}
)

func init() {
	RegisterResource(ResourceDefinition{Resource: "referral_withdrawals", LabelKey: "Referral withdrawals", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View referral withdrawals", DescriptionKey: "View referral withdrawal requests and payment evidence.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "review", LabelKey: "Review referral withdrawals", DescriptionKey: "Approve or reject pending referral withdrawals.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "pay", LabelKey: "Record referral payments", DescriptionKey: "Start manual payments and record completed transaction hashes.", DefaultRoles: []string{BuiltInRoleAdmin}},
	}})
	RegisterResource(ResourceDefinition{Resource: "referral_withdrawal_settings", LabelKey: "Referral withdrawal settings", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View withdrawal settings", DescriptionKey: "View referral withdrawal eligibility and network fees.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "update", LabelKey: "Update withdrawal settings", DescriptionKey: "Configure referral withdrawal eligibility and network fees.", DefaultRoles: []string{BuiltInRoleAdmin}},
	}})
}
